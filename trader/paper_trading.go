// Package trader - 本地模拟盘撮合引擎（Paper Trading）
// 用币安真实行情在本地模拟成交：市价按最新价±滑点成交、taker/maker 手续费、
// 真实资金费率每 8 小时结算、维持保证金率触顶强平。
// 实现统一 Trader 接口后，AI 决策链路与手动交易 API 零改动接入。
// 撮合引擎全权负责持仓(trader_positions)/订单(trader_orders)/成交(trader_fills)落库，
// 上层 recordAndConfirmOrder/OrderSync 在模拟盘模式下跳过以避免重复记录。
package trader

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"nofx/logger"
	"nofx/market"
	"nofx/store"
)

// 模拟盘撮合参数（标准版拟真）
const (
	PaperTakerFeeRate  = 0.0005 // taker 手续费 0.05%
	PaperMakerFeeRate  = 0.0002 // maker 手续费 0.02%
	PaperSlippageRate  = 0.0002 // 市价单固定滑点 0.02%
	PaperMaintMargin   = 0.005  // 维持保证金率 0.5%（逐仓）
	paperExchangeType  = "paper"
	paperDefaultFundingSettle = 8 * time.Hour
)

// PaperTrader 本地模拟盘交易执行器
type PaperTrader struct {
	store    *store.Store
	traderID string
	userID   string
	exchangeID string // 关联的交易所账户 UUID（记录溯源用）

	mu           sync.Mutex
	nextOrderID  int64
	recentOrders map[int64]*paperOrderResult // orderID -> 成交结果（GetOrderStatus 查询用）
	// SL/TP 挂单：key = SYMBOL|LONG/SHORT，value = 挂单价格（0=未设置）
	stopOrders map[string]*paperStopOrder
}

// paperStopOrder 本地止损/止盈挂单
type paperStopOrder struct {
	Symbol       string
	PositionSide string // LONG/SHORT
	Quantity     float64
	StopPrice    float64 // 止损触发价（0=未设置）
	TakeProfit   float64 // 止盈触发价（0=未设置）
}

// paperOrderResult 模拟订单成交结果
type paperOrderResult struct {
	status     string
	avgPrice   float64
	executedQty float64
	commission float64
}

// NewPaperTrader 创建本地模拟盘交易执行器
func NewPaperTrader(st *store.Store, traderID, userID, exchangeID string) *PaperTrader {
	return &PaperTrader{
		store:        st,
		traderID:     traderID,
		userID:       userID,
		exchangeID:   exchangeID,
		nextOrderID:  time.Now().UnixNano() % 1_000_000_000, // 本地订单号起点
		recentOrders: make(map[int64]*paperOrderResult),
		stopOrders:   make(map[string]*paperStopOrder),
	}
}

// ensureAccount 获取（或创建）模拟账户（调用方需持有 mu）
func (t *PaperTrader) ensureAccount() (*store.PaperAccount, error) {
	acc, err := t.store.PaperAccount().Get(t.traderID)
	if err != nil {
		return nil, err
	}
	if acc == nil {
		// 初始资金取 trader 配置的 InitialBalance（创建 trader 时用户自定义）
		initial := 1000.0
		if trader, err := t.store.Trader().GetByID(t.traderID); err == nil && trader != nil && trader.InitialBalance > 0 {
			initial = trader.InitialBalance
		}
		acc, err = t.store.PaperAccount().GetOrCreate(t.traderID, initial)
		if err != nil {
			return nil, err
		}
		logger.Infof("🎮 [Paper:%s] 模拟账户已创建，初始资金 %.2f USDT", t.traderID, acc.InitialBalance)
	}
	if acc.FundingLastSettle.IsZero() {
		acc.FundingLastSettle = time.Now().UTC()
	}
	return acc, nil
}

// settleFunding 资金费 lazy 结算：跨过 8 小时结算点（00/08/16 UTC）时按真实费率对全部持仓结算
func (t *PaperTrader) settleFunding(acc *store.PaperAccount, positions []*store.TraderPosition, priceFn func(string) (float64, error)) {
	now := time.Now().UTC()
	last := acc.FundingLastSettle
	if last.IsZero() {
		last = now
	}
	// 计算跨过的 8h 结算点数量（防重置后巨量回补，最多结算 1 个周期的费率）
	elapsed := now.Sub(last)
	if elapsed < paperDefaultFundingSettle {
		return
	}
	periods := int(elapsed / paperDefaultFundingSettle)
	if periods > 3 {
		periods = 3
	}
	total := 0.0
	for _, pos := range positions {
		if pos.Status != "OPEN" || pos.Quantity <= 0 {
			continue
		}
		rate, err := market.GetFundingRate(pos.Symbol)
		if err != nil {
			continue
		}
		markPrice, err := priceFn(pos.Symbol)
		if err != nil || markPrice <= 0 {
			markPrice = pos.EntryPrice
		}
		notional := markPrice * pos.Quantity
		dir := 1.0
		if pos.Side == "SHORT" {
			dir = -1.0
		}
		// 多头在费率为正时支付，空头反向收取
		payment := rate * notional * dir * float64(periods)
		total += payment
		if payment != 0 {
			logger.Infof("💰 [Paper:%s] 资金费结算 %s %s: %.4f USDT (rate=%.4f%%)", t.traderID, pos.Symbol, pos.Side, -payment, rate*100)
		}
	}
	if total != 0 {
		acc.Balance -= total
		acc.AccumulatedFunding += total
	}
	acc.FundingLastSettle = now
	_ = t.store.PaperAccount().Update(acc)
}

// getCurrentPrice 获取最新真实价格（币安 fapi ticker）
func (t *PaperTrader) getCurrentPrice(symbol string) (float64, error) {
	return market.NewAPIClient().GetCurrentPrice(symbol)
}

// paperPositionKey SL/TP 挂单 map key
func paperPositionKey(symbol, side string) string {
	return strings.ToUpper(symbol) + "|" + strings.ToUpper(side)
}

// checkLiquidation 检查并执行强平（逐仓维持保证金率触顶），返回是否发生强平
func (t *PaperTrader) checkLiquidation(pos *store.TraderPosition, markPrice float64) bool {
	if pos.Status != "OPEN" || pos.Quantity <= 0 || pos.Leverage <= 0 {
		return false
	}
	dir := 1.0
	if pos.Side == "SHORT" {
		dir = -1.0
	}
	margin := (pos.EntryPrice * pos.Quantity) / float64(pos.Leverage)
	unrealized := (markPrice - pos.EntryPrice) * pos.Quantity * dir
	notional := markPrice * pos.Quantity
	// 仓位权益 ≤ 维持保证金要求 → 强平
	if margin+unrealized > notional*PaperMaintMargin {
		return false
	}

	logger.Infof("💥 [Paper:%s] 强平触发: %s %s entry=%.4f mark=%.4f lev=%dx 权益=%.2f ≤ 名义×0.5%%",
		t.traderID, pos.Symbol, pos.Side, pos.EntryPrice, markPrice, pos.Leverage, margin+unrealized)

	// 先落库再动余额，避免落库失败后重复扣款
	fee := notional * PaperTakerFeeRate
	pnl := unrealized
	orderIDStr := fmt.Sprintf("%d", t.genOrderID())
	if err := t.store.Position().ClosePositionWithAccurateData(pos.ID, markPrice, orderIDStr, time.Now().UTC(), pnl, pos.Fee+fee, "risk_control"); err != nil {
		logger.Infof("⚠️ [Paper:%s] 强平落库失败: %v", t.traderID, err)
		return false
	}
	t.applyBalanceDelta(pnl - fee)
	// 记录强平订单与成交（action 用小写，与 getSideFromAction 约定一致）
	closeAction := "close_long"
	if pos.Side == "SHORT" {
		closeAction = "close_short"
	}
	t.recordOrderAndFill(pos.Symbol, closeAction, pos.Quantity, markPrice, fee, pnl, orderIDStr)
	// 标记强平史
	if acc, err := t.ensureAccount(); err == nil {
		acc.Liquidated = true
		_ = t.store.PaperAccount().Update(acc)
	}
	// 清挂单
	delete(t.stopOrders, paperPositionKey(pos.Symbol, pos.Side))
	return true
}

// applyBalanceDelta 余额变动（下限 0，不为负）
func (t *PaperTrader) applyBalanceDelta(delta float64) {
	acc, err := t.ensureAccount()
	if err != nil {
		return
	}
	acc.Balance += delta
	if acc.Balance < 0 {
		acc.Balance = 0
	}
	_ = t.store.PaperAccount().Update(acc)
}

// genOrderID 生成本地自增订单号
func (t *PaperTrader) genOrderID() int64 {
	t.nextOrderID++
	return t.nextOrderID
}

// recordOrderAndFill 记录模拟订单与成交（trader_orders + trader_fills）
func (t *PaperTrader) recordOrderAndFill(symbol, orderAction string, quantity, price, fee, realizedPnL float64, orderID string) {
	now := time.Now()
	side := getSideFromAction(orderAction)
	order := &store.TraderOrder{
		TraderID:        t.traderID,
		ExchangeID:      t.exchangeID,
		ExchangeType:    paperExchangeType,
		ExchangeOrderID: orderID,
		Symbol:          symbol,
		Side:            side,
		PositionSide:    positionSideFromAction(orderAction),
		Type:            "MARKET",
		TimeInForce:     "GTC",
		Quantity:        quantity,
		Status:          "FILLED",
		FilledQuantity:  quantity,
		AvgFillPrice:    price,
		Commission:      fee,
		CommissionAsset: "USDT",
		OrderAction:     orderAction,
		FilledAt:        now,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if err := t.store.Order().CreateOrder(order); err != nil {
		logger.Infof("⚠️ [Paper:%s] 订单落库失败: %v", t.traderID, err)
		return
	}
	fill := &store.TraderFill{
		TraderID:        t.traderID,
		ExchangeID:      t.exchangeID,
		ExchangeType:    paperExchangeType,
		OrderID:         order.ID,
		ExchangeOrderID: orderID,
		ExchangeTradeID: fmt.Sprintf("%s-%d", orderID, now.UnixNano()),
		Symbol:          symbol,
		Side:            side,
		Price:           price,
		Quantity:        quantity,
		QuoteQuantity:   price * quantity,
		Commission:      fee,
		CommissionAsset: "USDT",
		RealizedPnL:     realizedPnL,
		IsMaker:         false,
		CreatedAt:       now,
	}
	if err := t.store.Order().CreateFill(fill); err != nil {
		logger.Infof("⚠️ [Paper:%s] 成交落库失败: %v", t.traderID, err)
	}
}

// fillResult 构造 FILLED 订单返回值并缓存（GetOrderStatus 轮询用）
func (t *PaperTrader) fillResult(orderID int64, price, qty, fee float64) map[string]interface{} {
	t.recentOrders[orderID] = &paperOrderResult{status: "FILLED", avgPrice: price, executedQty: qty, commission: fee}
	return map[string]interface{}{
		"orderId":    orderID,
		"status":     "FILLED",
		"avgPrice":   price,
		"executedQty": qty,
		"commission": fee,
	}
}

// openPosition 市价开仓（多头/空头统一实现）
func (t *PaperTrader) openPosition(symbol string, quantity float64, leverage int, side string, makerFirst bool) (map[string]interface{}, error) {
	if quantity <= 0 {
		return nil, fmt.Errorf("quantity must be positive")
	}
	if leverage < 1 {
		leverage = 1
	}
	symbol = strings.ToUpper(symbol)

	t.mu.Lock()
	defer t.mu.Unlock()

	acc, err := t.ensureAccount()
	if err != nil {
		return nil, err
	}
	positions, err := t.store.Position().GetOpenPositions(t.traderID)
	if err != nil {
		return nil, err
	}
	t.settleFunding(acc, positions, t.getCurrentPrice)

	// 同向已有持仓则拒绝（上层决策链路已检查，这里兜底）
	posSide := "LONG"
	if side == "short" {
		posSide = "SHORT"
	}
	if existing, _ := t.store.Position().GetOpenPositionBySymbol(t.traderID, symbol, posSide); existing != nil {
		return nil, fmt.Errorf("%s already has %s position", symbol, posSide)
	}

	basePrice, err := t.getCurrentPrice(symbol)
	if err != nil {
		return nil, fmt.Errorf("failed to get market price: %w", err)
	}
	// 多头买贵 / 空头卖便宜
	price := basePrice * (1 + PaperSlippageRate)
	if side == "short" {
		price = basePrice * (1 - PaperSlippageRate)
	}
	feeRate := PaperTakerFeeRate
	if makerFirst {
		feeRate = PaperMakerFeeRate
	}
	notional := price * quantity
	fee := notional * feeRate

	// 保证金占用校验
	margin := notional / float64(leverage)
	marginUsed := t.marginInUse(positions)
	if acc.Balance-fee <= 0 {
		return nil, fmt.Errorf("insufficient balance: %.2f (fee %.2f)", acc.Balance, fee)
	}
	if marginUsed+margin > acc.Balance-fee {
		return nil, fmt.Errorf("insufficient margin: need %.2f, available %.2f (balance %.2f - fee %.2f)",
			margin, acc.Balance-fee-marginUsed, acc.Balance, fee)
	}

	orderID := t.genOrderID()
	orderIDStr := fmt.Sprintf("%d", orderID)
	entryTime := time.Now().UTC()

	pos := &store.TraderPosition{
		TraderID:      t.traderID,
		ExchangeID:    t.exchangeID,
		ExchangeType:  paperExchangeType,
		Symbol:        symbol,
		Side:          posSide,
		EntryQuantity: quantity,
		Quantity:      quantity,
		EntryPrice:    price,
		EntryOrderID:  orderIDStr,
		EntryTime:     entryTime,
		Fee:           fee,
		Leverage:      leverage,
		Status:        "OPEN",
		Source:        "system",
	}
	if err := t.store.Position().CreateOpenPosition(pos); err != nil {
		return nil, fmt.Errorf("failed to record paper position: %w", err)
	}

	acc.Balance -= fee
	acc.AccumulatedFee += fee
	if err := t.store.PaperAccount().Update(acc); err != nil {
		return nil, err
	}

	orderAction := "open_long"
	if side == "short" {
		orderAction = "open_short"
	}
	t.recordOrderAndFill(symbol, orderAction, quantity, price, fee, 0, orderIDStr)

	logger.Infof("🎮 [Paper:%s] 开%s %s qty=%.6f price=%.6f lev=%dx margin=%.2f fee=%.4f",
		t.traderID, posSide, symbol, quantity, price, leverage, margin, fee)

	return t.fillResult(orderID, price, quantity, fee), nil
}

// closePosition 市价平仓（quantity=0 全平），close reason 由 pending 机制传入
func (t *PaperTrader) closePosition(symbol string, quantity float64, side string) (map[string]interface{}, error) {
	symbol = strings.ToUpper(symbol)
	posSide := "LONG"
	if side == "short" {
		posSide = "SHORT"
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	acc, err := t.ensureAccount()
	if err != nil {
		return nil, err
	}
	positions, err := t.store.Position().GetOpenPositions(t.traderID)
	if err != nil {
		return nil, err
	}
	t.settleFunding(acc, positions, t.getCurrentPrice)

	pos, err := t.store.Position().GetOpenPositionBySymbol(t.traderID, symbol, posSide)
	if err != nil || pos == nil {
		return map[string]interface{}{"status": "NO_POSITION", "orderId": int64(0)}, nil
	}

	closeQty := pos.Quantity
	if quantity > 0 && quantity < pos.Quantity {
		closeQty = quantity
	}

	basePrice, err := t.getCurrentPrice(symbol)
	if err != nil {
		return nil, fmt.Errorf("failed to get market price: %w", err)
	}
	// 多头卖便宜 / 空头买回贵
	price := basePrice * (1 - PaperSlippageRate)
	if side == "short" {
		price = basePrice * (1 + PaperSlippageRate)
	}

	dir := 1.0
	if side == "short" {
		dir = -1.0
	}
	pnl := (price - pos.EntryPrice) * closeQty * dir
	fee := price * closeQty * PaperTakerFeeRate

	orderID := t.genOrderID()
	orderIDStr := fmt.Sprintf("%d", orderID)
	now := time.Now().UTC()

	// 平仓原因：程序侧/AI/手动平仓流程已通过 SetPendingCloseReason 登记
	closeReason := TakePendingCloseReason(t.traderID, symbol, posSide)
	if closeReason == "" {
		closeReason = "sync"
	}

	fullClose := closeQty >= pos.Quantity
	if fullClose {
		if err := t.store.Position().ClosePositionWithAccurateData(pos.ID, price, orderIDStr, now, pnl, pos.Fee+fee, closeReason); err != nil {
			return nil, fmt.Errorf("failed to close paper position: %w", err)
		}
		delete(t.stopOrders, paperPositionKey(symbol, posSide))
	} else {
		if err := t.store.Position().ReducePositionQuantity(pos.ID, closeQty, price, fee, pnl); err != nil {
			return nil, fmt.Errorf("failed to reduce paper position: %w", err)
		}
	}

	acc.Balance += pnl - fee
	if acc.Balance < 0 {
		acc.Balance = 0
	}
	acc.AccumulatedFee += fee
	if err := t.store.PaperAccount().Update(acc); err != nil {
		return nil, err
	}

	orderAction := "close_long"
	if side == "short" {
		orderAction = "close_short"
	}
	t.recordOrderAndFill(symbol, orderAction, closeQty, price, fee, pnl, orderIDStr)

	logger.Infof("🎮 [Paper:%s] 平%s %s qty=%.6f price=%.6f pnl=%.2f fee=%.4f reason=%s",
		t.traderID, posSide, symbol, closeQty, price, pnl, fee, closeReason)

	result := t.fillResult(orderID, price, closeQty, fee)
	result["realizedPnl"] = pnl
	return result, nil
}

// marginInUse 计算当前持仓占用保证金
func (t *PaperTrader) marginInUse(positions []*store.TraderPosition) float64 {
	total := 0.0
	for _, pos := range positions {
		if pos.Status != "OPEN" {
			continue
		}
		lev := pos.Leverage
		if lev < 1 {
			lev = 1
		}
		total += (pos.EntryPrice * pos.Quantity) / float64(lev)
	}
	return total
}

// unrealizedPnL 计算全部持仓未实现盈亏
func (t *PaperTrader) unrealizedPnL(positions []*store.TraderPosition) (float64, float64) {
	total := 0.0
	marginTotal := 0.0
	for _, pos := range positions {
		if pos.Status != "OPEN" {
			continue
		}
		mark, err := t.getCurrentPrice(pos.Symbol)
		if err != nil || mark <= 0 {
			mark = pos.EntryPrice
		}
		dir := 1.0
		if pos.Side == "SHORT" {
			dir = -1.0
		}
		lev := pos.Leverage
		if lev < 1 {
			lev = 1
		}
		total += (mark - pos.EntryPrice) * pos.Quantity * dir
		marginTotal += (pos.EntryPrice * pos.Quantity) / float64(lev)
	}
	return total, marginTotal
}

// ---------- Trader 接口实现 ----------

// GetBalance 获取模拟账户余额
func (t *PaperTrader) GetBalance() (map[string]interface{}, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	acc, err := t.ensureAccount()
	if err != nil {
		return nil, err
	}
	positions, err := t.store.Position().GetOpenPositions(t.traderID)
	if err != nil {
		return nil, err
	}
	t.settleFunding(acc, positions, t.getCurrentPrice)

	unrealized, marginUsed := t.unrealizedPnL(positions)
	available := acc.Balance - marginUsed
	if available < 0 {
		available = 0
	}
	return map[string]interface{}{
		"totalWalletBalance":   acc.Balance,
		"availableBalance":     available,
		"totalUnrealizedProfit": unrealized,
		"totalEquity":          acc.Balance + unrealized,
	}, nil
}

// GetPositions 获取全部模拟持仓（含强平检查；字段格式与币安一致）
func (t *PaperTrader) GetPositions() ([]map[string]interface{}, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	acc, err := t.ensureAccount()
	if err != nil {
		return nil, err
	}
	positions, err := t.store.Position().GetOpenPositions(t.traderID)
	if err != nil {
		return nil, err
	}
	t.settleFunding(acc, positions, t.getCurrentPrice)

	var result []map[string]interface{}
	for _, pos := range positions {
		if pos.Status != "OPEN" || pos.Quantity <= 0 {
			continue
		}
		mark, err := t.getCurrentPrice(pos.Symbol)
		if err != nil || mark <= 0 {
			mark = pos.EntryPrice
		}
		// 强平检查（维持保证金率触顶）
		if t.checkLiquidation(pos, mark) {
			continue
		}
		dir := 1.0
		amount := pos.Quantity
		side := "long"
		if pos.Side == "SHORT" {
			dir = -1.0
			amount = -pos.Quantity
			side = "short"
		}
		unrealized := (mark - pos.EntryPrice) * pos.Quantity * dir
		lev := pos.Leverage
		if lev < 1 {
			lev = 1
		}
		// 强平价估算：多头 entry×(1-1/lev+mmr)，空头 entry×(1+1/lev-mmr)
		liqPrice := pos.EntryPrice * (1 - 1/float64(lev) + PaperMaintMargin)
		if pos.Side == "SHORT" {
			liqPrice = pos.EntryPrice * (1 + 1/float64(lev) - PaperMaintMargin)
		}
		result = append(result, map[string]interface{}{
			"symbol":            pos.Symbol,
			"positionAmt":       amount,
			"entryPrice":        pos.EntryPrice,
			"markPrice":         mark,
			"unRealizedProfit":  unrealized,
			"leverage":          float64(lev),
			"liquidationPrice":  liqPrice,
			"side":              side,
		})
	}
	return result, nil
}

// OpenLong 市价开多
func (t *PaperTrader) OpenLong(symbol string, quantity float64, leverage int) (map[string]interface{}, error) {
	return t.openPosition(symbol, quantity, leverage, "long", false)
}

// OpenShort 市价开空
func (t *PaperTrader) OpenShort(symbol string, quantity float64, leverage int) (map[string]interface{}, error) {
	return t.openPosition(symbol, quantity, leverage, "short", false)
}

// CloseLong 平多（quantity=0 全平）
func (t *PaperTrader) CloseLong(symbol string, quantity float64) (map[string]interface{}, error) {
	return t.closePosition(symbol, quantity, "long")
}

// CloseShort 平空（quantity=0 全平）
func (t *PaperTrader) CloseShort(symbol string, quantity float64) (map[string]interface{}, error) {
	return t.closePosition(symbol, quantity, "short")
}

// OpenLongMakerFirst 模拟 maker 优先开多（本地立即成交，按 maker 费率）
func (t *PaperTrader) OpenLongMakerFirst(symbol string, quantity float64, leverage int, waitSeconds int) (map[string]interface{}, error) {
	return t.openPosition(symbol, quantity, leverage, "long", true)
}

// OpenShortMakerFirst 模拟 maker 优先开空（本地立即成交，按 maker 费率）
func (t *PaperTrader) OpenShortMakerFirst(symbol string, quantity float64, leverage int, waitSeconds int) (map[string]interface{}, error) {
	return t.openPosition(symbol, quantity, leverage, "short", true)
}

// SetLeverage 模拟盘无需设置（杠杆由开仓参数携带）
func (t *PaperTrader) SetLeverage(symbol string, leverage int) error { return nil }

// SetMarginMode 模拟盘无需设置
func (t *PaperTrader) SetMarginMode(symbol string, isCrossMargin bool) error { return nil }

// GetMarketPrice 获取真实市场最新价
func (t *PaperTrader) GetMarketPrice(symbol string) (float64, error) {
	return t.getCurrentPrice(symbol)
}

// SetStopLoss 设置本地止损挂单
func (t *PaperTrader) SetStopLoss(symbol string, positionSide string, quantity, stopPrice float64) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	key := paperPositionKey(symbol, positionSide)
	st, ok := t.stopOrders[key]
	if !ok {
		st = &paperStopOrder{Symbol: strings.ToUpper(symbol), PositionSide: strings.ToUpper(positionSide), Quantity: quantity}
		t.stopOrders[key] = st
	}
	st.Quantity = quantity
	st.StopPrice = stopPrice
	// 同步落库（手动调整 SL 时前端可见；AI 开仓流程的 persistInitialSLTP 写 initial 字段，互不冲突）
	if pos, err := t.store.Position().GetOpenPositionBySymbol(t.traderID, strings.ToUpper(symbol), strings.ToUpper(positionSide)); err == nil && pos != nil {
		tp := pos.FinalTakeProfit
		if err := t.store.Position().UpdateStopLossTakeProfit(pos.ID, stopPrice, tp); err != nil {
			logger.Infof("⚠️ [Paper:%s] 止损落库失败: %v", t.traderID, err)
		}
	}
	return nil
}

// SetTakeProfit 设置本地止盈挂单
func (t *PaperTrader) SetTakeProfit(symbol string, positionSide string, quantity, takeProfitPrice float64) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	key := paperPositionKey(symbol, positionSide)
	st, ok := t.stopOrders[key]
	if !ok {
		st = &paperStopOrder{Symbol: strings.ToUpper(symbol), PositionSide: strings.ToUpper(positionSide), Quantity: quantity}
		t.stopOrders[key] = st
	}
	st.Quantity = quantity
	st.TakeProfit = takeProfitPrice
	if pos, err := t.store.Position().GetOpenPositionBySymbol(t.traderID, strings.ToUpper(symbol), strings.ToUpper(positionSide)); err == nil && pos != nil {
		sl := pos.FinalStopLoss
		if err := t.store.Position().UpdateStopLossTakeProfit(pos.ID, sl, takeProfitPrice); err != nil {
			logger.Infof("⚠️ [Paper:%s] 止盈落库失败: %v", t.traderID, err)
		}
	}
	return nil
}

// CancelStopLossOrders 取消止损挂单（保留止盈）
func (t *PaperTrader) CancelStopLossOrders(symbol string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	for key, st := range t.stopOrders {
		if strings.ToUpper(symbol) == st.Symbol {
			st.StopPrice = 0
			if st.StopPrice == 0 && st.TakeProfit == 0 {
				delete(t.stopOrders, key)
			}
		}
	}
	return nil
}

// CancelTakeProfitOrders 取消止盈挂单（保留止损）
func (t *PaperTrader) CancelTakeProfitOrders(symbol string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	for key, st := range t.stopOrders {
		if strings.ToUpper(symbol) == st.Symbol {
			st.TakeProfit = 0
			if st.StopPrice == 0 && st.TakeProfit == 0 {
				delete(t.stopOrders, key)
			}
		}
	}
	return nil
}

// CancelAllOrders 取消该 symbol 全部挂单
func (t *PaperTrader) CancelAllOrders(symbol string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	for key, st := range t.stopOrders {
		if strings.ToUpper(symbol) == st.Symbol {
			delete(t.stopOrders, key)
		}
	}
	return nil
}

// CancelStopOrders 取消止损/止盈挂单
func (t *PaperTrader) CancelStopOrders(symbol string) error {
	return t.CancelAllOrders(symbol)
}

// FormatQuantity 格式化数量精度（按价格量级近似币安精度规则）
func (t *PaperTrader) FormatQuantity(symbol string, quantity float64) (string, error) {
	price, err := t.getCurrentPrice(symbol)
	if err != nil {
		return strconv.FormatFloat(quantity, 'f', 3, 64), nil
	}
	precision := 3
	switch {
	case price >= 10000:
		precision = 3
	case price >= 100:
		precision = 3
	case price >= 1:
		precision = 4
	default:
		precision = 6
	}
	return strconv.FormatFloat(quantity, 'f', precision, 64), nil
}

// GetOrderStatus 查询模拟订单状态（本地内存，立即 FILLED）
func (t *PaperTrader) GetOrderStatus(symbol string, orderID string) (map[string]interface{}, error) {
	id, err := strconv.ParseInt(orderID, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid paper order id: %s", orderID)
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if result, ok := t.recentOrders[id]; ok {
		return map[string]interface{}{
			"status":     result.status,
			"avgPrice":   result.avgPrice,
			"executedQty": result.executedQty,
			"commission": result.commission,
		}, nil
	}
	// 进程重启后内存丢失，查订单库恢复
	if order, err := t.store.Order().GetOrderByExchangeID(t.exchangeID, orderID); err == nil && order != nil {
		return map[string]interface{}{
			"status":     order.Status,
			"avgPrice":   order.AvgFillPrice,
			"executedQty": order.FilledQuantity,
			"commission": order.Commission,
		}, nil
	}
	return nil, fmt.Errorf("paper order %s not found", orderID)
}

// GetClosedPnL 返回本地已平仓记录（模拟盘无交易所侧历史，直接从本地库映射）
func (t *PaperTrader) GetClosedPnL(startTime time.Time, limit int) ([]ClosedPnLRecord, error) {
	positions, err := t.store.Position().GetClosedPositions(t.traderID, limit)
	if err != nil {
		return nil, err
	}
	var result []ClosedPnLRecord
	for _, pos := range positions {
		exitTime := time.Now()
		if pos.ExitTime != nil {
			exitTime = *pos.ExitTime
		}
		if !exitTime.After(startTime) {
			continue
		}
		result = append(result, ClosedPnLRecord{
			Symbol:      pos.Symbol,
			Side:        strings.ToLower(pos.Side),
			EntryPrice:  pos.EntryPrice,
			ExitPrice:   pos.ExitPrice,
			Quantity:    pos.Quantity,
			RealizedPnL: pos.RealizedPnL,
			Fee:         pos.Fee,
			Leverage:    pos.Leverage,
			EntryTime:   pos.EntryTime,
			ExitTime:    exitTime,
			OrderID:     pos.ExitOrderID,
			CloseType:   pos.CloseReason,
			ExchangeID:  t.exchangeID,
		})
	}
	return result, nil
}

// positionSideFromAction 从订单动作推导持仓方向
func positionSideFromAction(action string) string {
	if strings.Contains(strings.ToUpper(action), "SHORT") {
		return "SHORT"
	}
	return "LONG"
}
