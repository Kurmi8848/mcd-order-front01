package main

import (
	"database/sql"
	"fmt"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

var db *sql.DB

const (
	StatusReceived  = "オーダ受信済み"
	StatusCooking   = "調理済み"
	StatusDelivered = "受け渡し済み"
)

func initDB() {
	// 同時書き込み対策のbusy_timeoutと、MaxOpenConns=1の設定
	var err error
	db, err = sql.Open("sqlite3", "order.db?_busy_timeout=5000")
	if err != nil {
		logger.Fatalf("[DB_ERROR] Failed to open database: %v", err)
	}

	db.SetMaxOpenConns(1)

	schema := `
	CREATE TABLE IF NOT EXISTS order_items (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		order_no TEXT NOT NULL,
		terminal_no TEXT NOT NULL,
		order_status TEXT NOT NULL,
		item_no INTEGER NOT NULL,
		menu_name TEXT NOT NULL,
		unit_price INTEGER NOT NULL,
		quantity INTEGER NOT NULL,
		subtotal INTEGER NOT NULL,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);`

	if _, err := db.Exec(schema); err != nil {
		logger.Fatalf("[DB_ERROR] Failed to create schema: %v", err)
	}
	logger.Println("[DB] Database and schema initialized successfully.")
}

func closeDB() {
	if db != nil {
		db.Close()
		logger.Println("[DB] Database connection closed.")
	}
}

// 採番処理と注文データ登録を同一トランザクション内で実行
func insertOrderTx(terminalNo string, items []OrderItemInput) (string, error) {
	tx, err := db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()

	todayStr := time.Now().Format("0102") // MMDD形式

	// 本日の最大注文番号を取得（排他ロックを兼ねる）
	var maxOrderNo sql.NullString
	query := `SELECT MAX(order_no) FROM order_items WHERE order_no LIKE ?`
	err = tx.QueryRow(query, todayStr+"-%").Scan(&maxOrderNo)
	if err != nil {
		return "", err
	}

	nextSeq := 1
	if maxOrderNo.Valid {
		var lastSeq int
		_, err := fmt.Sscanf(maxOrderNo.String, todayStr+"-%d", &lastSeq)
		if err == nil {
			nextSeq = lastSeq + 1
		}
	}

	orderNo := fmt.Sprintf("%s-%03d", todayStr, nextSeq)

	// 明細のINSERT
	insertQuery := `
	INSERT INTO order_items (order_no, terminal_no, order_status, item_no, menu_name, unit_price, quantity, subtotal)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?)`

	for i, item := range items {
		itemNo := i + 1
		subtotal := item.UnitPrice * item.Quantity
		_, err := tx.Exec(insertQuery, orderNo, terminalNo, StatusReceived, itemNo, item.MenuName, item.UnitPrice, item.Quantity, subtotal)
		if err != nil {
			return "", err
		}
	}

	if err := tx.Commit(); err != nil {
		return "", err
	}

	return orderNo, nil
}