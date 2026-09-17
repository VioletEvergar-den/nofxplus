// Package store - 本地模拟盘账户存储
// 每个 paper trader 一套独立模拟资金账户（初始资金可自定义，支持一键重置）
package store

import (
	"database/sql"
	"fmt"
	"time"
)

// PaperAccount 本地模拟盘账户
type PaperAccount struct {
	TraderID           string    `json:"trader_id"`
	InitialBalance     float64   `json:"initial_balance"`      // 初始资金（可自定义）
	Balance            float64   `json:"balance"`              // 当前钱包余额（已扣手续费/已结算资金费/已入账已实现盈亏）
	AccumulatedFee     float64   `json:"accumulated_fee"`      // 累计手续费
	AccumulatedFunding float64   `json:"accumulated_funding"`  // 累计资金费（正=支出）
	FundingLastSettle  time.Time `json:"funding_last_settle"`  // 上次资金费结算时间
	Liquidated         bool      `json:"liquidated"`           // 是否发生过强平
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

// PaperAccountStore 模拟盘账户存储
type PaperAccountStore struct {
	db *sql.DB
}

// NewPaperAccountStore 创建模拟盘账户存储
func NewPaperAccountStore(db *sql.DB) *PaperAccountStore {
	return &PaperAccountStore{db: db}
}

// initTables 初始化模拟盘账户表
func (s *PaperAccountStore) initTables() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS paper_accounts (
			trader_id TEXT PRIMARY KEY,
			initial_balance REAL NOT NULL,
			balance REAL NOT NULL,
			accumulated_fee REAL DEFAULT 0,
			accumulated_funding REAL DEFAULT 0,
			funding_last_settle DATETIME,
			liquidated INTEGER DEFAULT 0,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)
	`)
	if err != nil {
		return fmt.Errorf("failed to create paper_accounts table: %w", err)
	}
	return nil
}

// Get 获取模拟盘账户（不存在返回 nil, nil）
func (s *PaperAccountStore) Get(traderID string) (*PaperAccount, error) {
	var acc PaperAccount
	var fundingLastSettle, createdAt, updatedAt sql.NullString
	err := s.db.QueryRow(`
		SELECT trader_id, initial_balance, balance, accumulated_fee, accumulated_funding,
		       funding_last_settle, liquidated, created_at, updated_at
		FROM paper_accounts WHERE trader_id = ?
	`, traderID).Scan(
		&acc.TraderID, &acc.InitialBalance, &acc.Balance, &acc.AccumulatedFee,
		&acc.AccumulatedFunding, &fundingLastSettle, &acc.Liquidated, &createdAt, &updatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get paper account: %w", err)
	}
	_ = parsePaperTime(&acc.FundingLastSettle, fundingLastSettle)
	_ = parsePaperTime(&acc.CreatedAt, createdAt)
	_ = parsePaperTime(&acc.UpdatedAt, updatedAt)
	return &acc, nil
}

// GetOrCreate 获取或创建模拟盘账户（创建时以 initialBalance 为初始资金）
func (s *PaperAccountStore) GetOrCreate(traderID string, initialBalance float64) (*PaperAccount, error) {
	acc, err := s.Get(traderID)
	if err != nil {
		return nil, err
	}
	if acc != nil {
		return acc, nil
	}
	if initialBalance <= 0 {
		initialBalance = 1000 // 默认初始资金
	}
	now := time.Now()
	_, err = s.db.Exec(`
		INSERT INTO paper_accounts (trader_id, initial_balance, balance, funding_last_settle, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, traderID, initialBalance, initialBalance, now.UTC(), now.UTC(), now.UTC())
	if err != nil {
		return nil, fmt.Errorf("failed to create paper account: %w", err)
	}
	return s.Get(traderID)
}

// Update 更新模拟盘账户（余额/累计费用等）
func (s *PaperAccountStore) Update(acc *PaperAccount) error {
	_, err := s.db.Exec(`
		UPDATE paper_accounts
		SET balance = ?, accumulated_fee = ?, accumulated_funding = ?, funding_last_settle = ?, liquidated = ?, updated_at = ?
		WHERE trader_id = ?
	`, acc.Balance, acc.AccumulatedFee, acc.AccumulatedFunding, acc.FundingLastSettle.UTC(), acc.Liquidated, time.Now().UTC(), acc.TraderID)
	if err != nil {
		return fmt.Errorf("failed to update paper account: %w", err)
	}
	return nil
}

// Reset 重置模拟账户：余额回初始资金、清累计费用，并删除该交易员的全部持仓/挂单/成交记录
func (s *PaperAccountStore) Reset(traderID string) error {
	if _, err := s.db.Exec(`
		UPDATE paper_accounts
		SET balance = initial_balance, accumulated_fee = 0, accumulated_funding = 0,
		    funding_last_settle = ?, liquidated = 0, updated_at = ?
		WHERE trader_id = ?
	`, time.Now().UTC(), time.Now().UTC(), traderID); err != nil {
		return fmt.Errorf("failed to reset paper account: %w", err)
	}
	// 清交易记录（trader_positions/trader_orders/trader_fills 按 trader_id 关联）
	for _, table := range []string{"trader_positions", "trader_orders", "trader_fills"} {
		if _, err := s.db.Exec(fmt.Sprintf(`DELETE FROM %s WHERE trader_id = ?`, table), traderID); err != nil {
			return fmt.Errorf("failed to clear %s for paper reset: %w", table, err)
		}
	}
	return nil
}

// parsePaperTime 解析 SQLite 时间字符串
func parsePaperTime(dst *time.Time, s sql.NullString) error {
	if !s.Valid || s.String == "" {
		return nil
	}
	for _, layout := range []string{"2006-01-02 15:04:05.999999999-07:00", "2006-01-02T15:04:05.999999999Z", "2006-01-02 15:04:05"} {
		if t, err := time.Parse(layout, s.String); err == nil {
			*dst = t
			return nil
		}
	}
	return nil
}
