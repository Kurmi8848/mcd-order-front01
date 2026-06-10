package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// --- 構造体定義 ---
type OrderItemInput struct {
	MenuName  string `json:"menuName"`
	UnitPrice int    `json:"unitPrice"`
	Quantity  int    `json:"quantity"`
	Subtotal  int    `json:"subtotal"`
}

type OrderCreateRequest struct {
	MessageType string           `json:"messageType"`
	TerminalNo  string           `json:"terminalNo"`
	TotalAmount int              `json:"totalAmount"`
	Items       []OrderItemInput `json:"items"`
}

type CommonResponse struct {
	Result      string `json:"result"`
	OrderNo     string `json:"orderNo,omitempty"`
	OrderStatus string `json:"orderStatus,omitempty"`
	TotalAmount int    `json:"totalAmount,omitempty"`
	Message     string `json:"message,omitempty"`
}

type OrderStatusUpdateRequest struct {
	OrderStatus string `json:"orderStatus"`
}

type BoardRequest struct {
	TerminalNo  string `json:"terminalNo"`
	MessageType string `json:"messageType"`
	OrderNo     string `json:"orderNo,omitempty"`
}

type BoardResponse struct {
	Result        string   `json:"result"`
	CookingOrders []string `json:"cookingOrders"`
	ReadyOrders   []string `json:"readyOrders"`
}

type KitchenRequest struct {
	TerminalNo  string `json:"terminalNo,omitempty"`
	MessageType string `json:"messageType"`
	OrderNo     string `json:"orderNo,omitempty"`
}

type KitchenItem struct {
	MenuName string `json:"menuName"`
	Quantity int    `json:"quantity"`
}

type KitchenOrder struct {
	OrderNo string        `json:"orderNo"`
	Items   []KitchenItem `json:"items"`
}

type KitchenResponse struct {
	Result string         `json:"result"`
	Orders []KitchenOrder `json:"orders"`
}

type OrderSummary struct {
	OrderNo     string           `json:"orderNo"`
	TerminalNo  string           `json:"terminalNo"`
	OrderStatus string           `json:"orderStatus"`
	TotalAmount int              `json:"totalAmount"`
	Items       []OrderItemInput `json:"items"`
}

// --- ミドルウェア ---
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		logger.Printf("[REQ_IN] Method: %s, Path: %s", r.Method, r.URL.String())
		next.ServeHTTP(w, r)
	})
}

// --- ヘルパー関数 ---
func respondWithError(w http.ResponseWriter, code int, msg string) {
	logger.Printf("[REQ_OUT] Error Status: %d, Message: %s", code, msg)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"result": "NG", "message": msg})
}

func respondWithJSON(w http.ResponseWriter, code int, payload interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	respBytes, _ := json.Marshal(payload)
	logger.Printf("[REQ_OUT] Success Status: %d, Payload: %s", code, string(respBytes))
	w.Write(respBytes)
}

// --- ハンドラ実装 ---

// POST /api/orders
func handleCreateOrder(w http.ResponseWriter, r *http.Request) {
	var req OrderCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid JSON payload")
		return
	}

	// 入力チェック
	if req.TerminalNo == "" {
		respondWithError(w, http.StatusBadRequest, "terminalNo is required")
		return
	}
	if req.MessageType != "ORDER_CONFIRM" {
		respondWithError(w, http.StatusBadRequest, "messageType must be ORDER_CONFIRM")
		return
	}
	if req.TotalAmount < 1 {
		respondWithError(w, http.StatusBadRequest, "totalAmount must be >= 1")
		return
	}
	if len(req.Items) < 1 || len(req.Items) > 5 {
		respondWithError(w, http.StatusBadRequest, "items count must be between 1 and 5")
		return
	}

	calcTotal := 0
	menuSet := make(map[string]bool)

	for _, item := range req.Items {
		if item.MenuName == "" {
			respondWithError(w, http.StatusBadRequest, "menuName is required in items")
			return
		}
		if item.UnitPrice < 1 {
			respondWithError(w, http.StatusBadRequest, "unitPrice must be >= 1")
			return
		}
		if item.Quantity < 1 || item.Quantity > 5 {
			respondWithError(w, http.StatusBadRequest, "quantity must be between 1 and 5")
			return
		}
		if menuSet[item.MenuName] {
			respondWithError(w, http.StatusBadRequest, "duplicate menuName is forbidden within one order")
			return
		}
		menuSet[item.MenuName] = true

		expectedSubtotal := item.UnitPrice * item.Quantity
		if item.Subtotal != expectedSubtotal {
			respondWithError(w, http.StatusBadRequest, fmt.Sprintf("subtotal mismatch for %s", item.MenuName))
			return
		}
		calcTotal += expectedSubtotal
	}

	if req.TotalAmount != calcTotal {
		respondWithError(w, http.StatusBadRequest, "totalAmount does not match sum of subtotals")
		return
	}

	// トランザクションによる登録処理
	orderNo, err := insertOrderTx(req.TerminalNo, req.Items)
	if err != nil {
		logger.Printf("[DB_ERROR] Order insertion failed: %v", err)
		respondWithError(w, http.StatusInternalServerError, "Database error")
		return
	}

	logger.Printf("[DB_INSERT] Created OrderNo: %s, TerminalNo: %s", orderNo, req.TerminalNo)

	resp := CommonResponse{
		Result:      "OK",
		OrderNo:     orderNo,
		OrderStatus: StatusReceived,
		TotalAmount: req.TotalAmount,
		Message:     "注文を正常に受信しました。",
	}
	respondWithJSON(w, http.StatusOK, resp)
}

// GET /api/orders & GET /api/orders?status=xxx
func handleListOrders(w http.ResponseWriter, r *http.Request) {
	statusFilter := r.URL.Query().Get("status")

	query := `SELECT order_no, terminal_no, order_status, menu_name, unit_price, quantity, subtotal FROM order_items`
	var args []interface{}

	if statusFilter != "" {
		query += ` WHERE order_status = ?`
		args = append(args, statusFilter)
	}
	query += ` ORDER BY order_no ASC, item_no ASC`

	rows, err := db.Query(query, args...)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Database error")
		return
	}
	defer rows.Close()

	orderMap := make(map[string]*OrderSummary)
	var orderOrder []string // 結果の順序を保持

	for rows.Next() {
		var oNo, tNo, oStatus, mName string
		var uPrice, qty, sub int
		if err := rows.Scan(&oNo, &tNo, &oStatus, &mName, &uPrice, &qty, &sub); err != nil {
			respondWithError(w, http.StatusInternalServerError, "Database scanning error")
			return
		}

		if _, exists := orderMap[oNo]; !exists {
			orderMap[oNo] = &OrderSummary{
				OrderNo:     oNo,
				TerminalNo:  tNo,
				OrderStatus: oStatus,
				TotalAmount: 0,
				Items:       []OrderItemInput{},
			}
			orderOrder = append(orderOrder, oNo)
		}
		orderMap[oNo].Items = append(orderMap[oNo].Items, OrderItemInput{
			MenuName:  mName,
			UnitPrice: uPrice,
			Quantity:  qty,
			Subtotal:  sub,
		})
		orderMap[oNo].TotalAmount += sub
	}

	results := []OrderSummary{}
	for _, oNo := range orderOrder {
		results = append(results, *orderMap[oNo])
	}

	respondWithJSON(w, http.StatusOK, results)
}

// GET /api/orders/{orderNo}
func handleGetOrder(w http.ResponseWriter, r *http.Request) {
	orderNo := r.PathValue("orderNo")
	if orderNo == "" {
		respondWithError(w, http.StatusBadRequest, "orderNo is required")
		return
	}

	query := `SELECT menu_name, unit_price, quantity, subtotal FROM order_items WHERE order_no = ? ORDER BY item_no ASC`
	rows, err := db.Query(query, orderNo)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Database error")
		return
	}
	defer rows.Close()

	items := []OrderItemInput{}
	for rows.Next() {
		var mName string
		var uPrice, qty, sub int
		if err := rows.Scan(&mName, &uPrice, &qty, &sub); err != nil {
			respondWithError(w, http.StatusInternalServerError, "Database error")
			return
		}
		items = append(items, OrderItemInput{
			MenuName:  mName,
			UnitPrice: uPrice,
			Quantity:  qty,
			Subtotal:  sub,
		})
	}

	if len(items) == 0 {
		respondWithError(w, http.StatusNotFound, "Order not found")
		return
	}

	respondWithJSON(w, http.StatusOK, items)
}

// PUT /api/orders/{orderNo}/status
func handleUpdateOrderStatus(w http.ResponseWriter, r *http.Request) {
	orderNo := r.PathValue("orderNo")
	if orderNo == "" {
		respondWithError(w, http.StatusBadRequest, "orderNo is required")
		return
	}

	var req OrderStatusUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid JSON")
		return
	}

	if req.OrderStatus != StatusReceived && req.OrderStatus != StatusCooking && req.OrderStatus != StatusDelivered {
		respondWithError(w, http.StatusBadRequest, "Invalid orderStatus value")
		return
	}

	result, err := db.Exec(`UPDATE order_items SET order_status = ? WHERE order_no = ?`, req.OrderStatus, orderNo)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Database error")
		return
	}

	affected, _ := result.RowsAffected()
	if affected == 0 {
		respondWithError(w, http.StatusNotFound, "Order not found")
		return
	}

	logger.Printf("[DB_UPDATE] Status updated. OrderNo: %s, NewStatus: %s", orderNo, req.OrderStatus)
	respondWithJSON(w, http.StatusOK, map[string]string{"result": "OK", "orderNo": orderNo})
}

// POST /api/board
func handleBoard(w http.ResponseWriter, r *http.Request) {
	var req BoardRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid JSON")
		return
	}

	if req.MessageType != "BOARD_REQUEST" {
		respondWithError(w, http.StatusBadRequest, "messageType must be BOARD_REQUEST")
		return
	}

	// orderNo 指定時はステータスを「受け渡し済み」に更新
	if req.OrderNo != "" {
		res, err := db.Exec(`UPDATE order_items SET order_status = ? WHERE order_no = ?`, StatusDelivered, req.OrderNo)
		if err != nil {
			respondWithError(w, http.StatusInternalServerError, "Database error")
			return
		}
		if aff, _ := res.RowsAffected(); aff > 0 {
			logger.Printf("[DB_UPDATE] Board action. OrderNo: %s updated to %s", req.OrderNo, StatusDelivered)
		}
	}

	// 最新の掲示板情報を取得
	cookingOrders := []string{}
	readyOrders := []string{}

	rows, err := db.Query(`SELECT DISTINCT order_no, order_status FROM order_items WHERE order_status IN (?, ?) ORDER BY order_no ASC`, StatusReceived, StatusCooking)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Database error")
		return
	}
	defer rows.Close()

	for rows.Next() {
		var oNo, oStatus string
		if err := rows.Scan(&oNo, &oStatus); err != nil {
			continue
		}
		if oStatus == StatusReceived {
			cookingOrders = append(cookingOrders, oNo)
		} else if oStatus == StatusCooking {
			readyOrders = append(readyOrders, oNo)
		}
	}

	respondWithJSON(w, http.StatusOK, BoardResponse{
		Result:        "OK",
		CookingOrders: cookingOrders,
		ReadyOrders:   readyOrders,
	})
}

// POST /api/kitchen
func handleKitchen(w http.ResponseWriter, r *http.Request) {
	var req KitchenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid JSON")
		return
	}

	if req.MessageType != "KITCHEN_REQUEST" {
		respondWithError(w, http.StatusBadRequest, "messageType must be KITCHEN_REQUEST")
		return
	}

	// orderNo 指定時はステータスを「調理済み」に更新
	if req.OrderNo != "" {
		res, err := db.Exec(`UPDATE order_items SET order_status = ? WHERE order_no = ?`, StatusCooking, req.OrderNo)
		if err != nil {
			respondWithError(w, http.StatusInternalServerError, "Database error")
			return
		}
		if aff, _ := res.RowsAffected(); aff > 0 {
			logger.Printf("[DB_UPDATE] Kitchen action. OrderNo: %s updated to %s", req.OrderNo, StatusCooking)
		}
	}

	// 「オーダ受信済み」の未調理一覧を取得
	rows, err := db.Query(`SELECT order_no, menu_name, quantity FROM order_items WHERE order_status = ? ORDER BY order_no ASC, item_no ASC`, StatusReceived)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Database error")
		return
	}
	defer rows.Close()

	kitchenMap := make(map[string]*KitchenOrder)
	var orderOrder []string

	for rows.Next() {
		var oNo, mName string
		var qty int
		if err := rows.Scan(&oNo, &mName, &qty); err != nil {
			continue
		}

		if _, exists := kitchenMap[oNo]; !exists {
			kitchenMap[oNo] = &KitchenOrder{
				OrderNo: oNo,
				Items:   []KitchenItem{},
			}
			orderOrder = append(orderOrder, oNo)
		}
		kitchenMap[oNo].Items = append(kitchenMap[oNo].Items, KitchenItem{
			MenuName: mName,
			Quantity: qty,
		})
	}

	orders := []KitchenOrder{}
	for _, oNo := range orderOrder {
		orders = append(orders, *kitchenMap[oNo])
	}

	respondWithJSON(w, http.StatusOK, KitchenResponse{
		Result: "OK",
		Orders: orders,
	})
}