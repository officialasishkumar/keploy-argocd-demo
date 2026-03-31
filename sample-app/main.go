package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"
)

type Order struct {
	ID        string    `json:"id"`
	Product   string    `json:"product"`
	Quantity  int       `json:"quantity"`
	Price     float64   `json:"price"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}

var (
	orders    = make(map[string]Order)
	mu        sync.RWMutex
	idCounter int
)

func main() {
	seedData()

	http.HandleFunc("/healthz", handleHealth)
	http.HandleFunc("/api/orders", handleOrders)
	http.HandleFunc("/api/orders/", handleOrderByID)

	port := ":8080"
	log.Printf("Sample Order Service starting on %s", port)
	log.Fatal(http.ListenAndServe(port, nil))
}

func seedData() {
	orders["1"] = Order{ID: "1", Product: "Laptop", Quantity: 2, Price: 999.99, Status: "completed", CreatedAt: time.Now()}
	orders["2"] = Order{ID: "2", Product: "Keyboard", Quantity: 5, Price: 49.99, Status: "pending", CreatedAt: time.Now()}
	orders["3"] = Order{ID: "3", Product: "Monitor", Quantity: 1, Price: 399.99, Status: "shipped", CreatedAt: time.Now()}
	idCounter = 3
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "healthy", "service": "sample-order-service"})
}

func handleOrders(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		mu.RLock()
		result := make([]Order, 0, len(orders))
		for _, o := range orders {
			result = append(result, o)
		}
		mu.RUnlock()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(result)

	case http.MethodPost:
		var o Order
		if err := json.NewDecoder(r.Body).Decode(&o); err != nil {
			http.Error(w, `{"error": "invalid JSON"}`, http.StatusBadRequest)
			return
		}
		mu.Lock()
		idCounter++
		o.ID = fmt.Sprintf("%d", idCounter)
		o.CreatedAt = time.Now()
		if o.Status == "" {
			o.Status = "pending"
		}
		orders[o.ID] = o
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(o)

	default:
		http.Error(w, `{"error": "method not allowed"}`, http.StatusMethodNotAllowed)
	}
}

func handleOrderByID(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Path[len("/api/orders/"):]
	if id == "" {
		http.Error(w, `{"error": "missing order ID"}`, http.StatusBadRequest)
		return
	}

	mu.RLock()
	order, exists := orders[id]
	mu.RUnlock()

	if !exists {
		http.Error(w, `{"error": "order not found"}`, http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(order)
}
