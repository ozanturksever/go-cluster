package cluster_test

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ozanturksever/go-cluster"
	"github.com/ozanturksever/go-cluster/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// E2E Test: Cross-App Communication Flow
// =============================================================================
// This test simulates a realistic microservice architecture where:
// - OrderService: Receives orders, validates with InventoryService, processes payment via PaymentService
// - InventoryService: Manages inventory, publishes stock events
// - PaymentService: Processes payments, publishes payment events
// - NotificationService: Listens to events and sends notifications
// =============================================================================

// Request/Response types for the test
type CreateOrderRequest struct {
	OrderID   string  `json:"order_id"`
	ProductID string  `json:"product_id"`
	Quantity  int     `json:"quantity"`
	Amount    float64 `json:"amount"`
}

type CreateOrderResponse struct {
	OrderID string `json:"order_id"`
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
}

type CheckInventoryRequest struct {
	ProductID string `json:"product_id"`
	Quantity  int    `json:"quantity"`
}

type CheckInventoryResponse struct {
	Available bool `json:"available"`
	Stock     int  `json:"stock"`
}

type ProcessPaymentRequest struct {
	OrderID string  `json:"order_id"`
	Amount  float64 `json:"amount"`
}

type ProcessPaymentResponse struct {
	TransactionID string `json:"transaction_id"`
	Status        string `json:"status"`
}

func TestE2E_CrossAppCommunicationFlow(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping E2E test in short mode")
	}

	ns := testutil.StartNATS(t)
	defer ns.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Create platform
	platform, err := cluster.NewPlatform("ecommerce", "node-1", ns.URL(),
		cluster.HealthAddr(":38480"),
		cluster.MetricsAddr(":39490"),
		cluster.WithLeaseTTL(2*time.Second),
		cluster.WithHeartbeat(500*time.Millisecond),
	)
	require.NoError(t, err)

	// Track events received by notification service
	var notificationCount atomic.Int32

	// Inventory state
	inventory := map[string]int{
		"PROD-001": 100,
		"PROD-002": 5,
		"PROD-003": 0,
	}
	var inventoryMu sync.RWMutex

	// =========================================================================
	// Create InventoryService (Singleton)
	// =========================================================================
	inventoryApp := cluster.NewApp("inventory", cluster.Singleton())

	inventoryApp.Handle("check", func(ctx context.Context, req []byte) ([]byte, error) {
		var checkReq CheckInventoryRequest
		if err := json.Unmarshal(req, &checkReq); err != nil {
			return nil, err
		}

		inventoryMu.RLock()
		stock := inventory[checkReq.ProductID]
		inventoryMu.RUnlock()

		resp := CheckInventoryResponse{
			Available: stock >= checkReq.Quantity,
			Stock:     stock,
		}
		return json.Marshal(resp)
	})

	inventoryApp.Handle("reserve", func(ctx context.Context, req []byte) ([]byte, error) {
		var checkReq CheckInventoryRequest
		if err := json.Unmarshal(req, &checkReq); err != nil {
			return nil, err
		}

		inventoryMu.Lock()
		stock := inventory[checkReq.ProductID]
		if stock < checkReq.Quantity {
			inventoryMu.Unlock()
			return nil, fmt.Errorf("insufficient stock")
		}
		inventory[checkReq.ProductID] = stock - checkReq.Quantity
		newStock := inventory[checkReq.ProductID]
		inventoryMu.Unlock()

		// Publish inventory event
		eventData, _ := json.Marshal(map[string]any{
			"product_id": checkReq.ProductID,
			"reserved":   checkReq.Quantity,
			"remaining":  newStock,
		})
		platform.Events().PublishFrom(ctx, "inventory", "inventory.reserved", eventData)

		return json.Marshal(CheckInventoryResponse{Available: true, Stock: newStock})
	})

	platform.Register(inventoryApp)

	// =========================================================================
	// Create PaymentService (Singleton)
	// =========================================================================
	paymentApp := cluster.NewApp("payment", cluster.Singleton())

	paymentApp.Handle("process", func(ctx context.Context, req []byte) ([]byte, error) {
		var payReq ProcessPaymentRequest
		if err := json.Unmarshal(req, &payReq); err != nil {
			return nil, err
		}

		// Simulate payment processing
		transactionID := fmt.Sprintf("TXN-%s-%d", payReq.OrderID, time.Now().UnixNano())

		// Publish payment event
		eventData, _ := json.Marshal(map[string]any{
			"order_id":       payReq.OrderID,
			"transaction_id": transactionID,
			"amount":         payReq.Amount,
			"status":         "completed",
		})
		platform.Events().PublishFrom(ctx, "payment", "payment.completed", eventData)

		return json.Marshal(ProcessPaymentResponse{
			TransactionID: transactionID,
			Status:        "completed",
		})
	})

	platform.Register(paymentApp)

	// =========================================================================
	// Create OrderService (Singleton) - Orchestrates cross-app calls
	// =========================================================================
	orderApp := cluster.NewApp("order", cluster.Singleton())

	orderApp.Handle("create", func(ctx context.Context, req []byte) ([]byte, error) {
		var orderReq CreateOrderRequest
		if err := json.Unmarshal(req, &orderReq); err != nil {
			return nil, err
		}

		// Step 1: Check inventory via cross-app call
		inventoryClient := platform.API("inventory")
		var inventoryResp CheckInventoryResponse
		err := inventoryClient.CallJSON(ctx, "check", CheckInventoryRequest{
			ProductID: orderReq.ProductID,
			Quantity:  orderReq.Quantity,
		}, &inventoryResp, cluster.ToLeader())
		if err != nil {
			return json.Marshal(CreateOrderResponse{
				OrderID: orderReq.OrderID,
				Status:  "failed",
				Message: fmt.Sprintf("inventory check failed: %v", err),
			})
		}

		if !inventoryResp.Available {
			return json.Marshal(CreateOrderResponse{
				OrderID: orderReq.OrderID,
				Status:  "rejected",
				Message: "insufficient inventory",
			})
		}

		// Step 2: Reserve inventory
		err = inventoryClient.CallJSON(ctx, "reserve", CheckInventoryRequest{
			ProductID: orderReq.ProductID,
			Quantity:  orderReq.Quantity,
		}, &inventoryResp, cluster.ToLeader())
		if err != nil {
			return json.Marshal(CreateOrderResponse{
				OrderID: orderReq.OrderID,
				Status:  "failed",
				Message: fmt.Sprintf("inventory reservation failed: %v", err),
			})
		}

		// Step 3: Process payment via cross-app call
		paymentClient := platform.API("payment")
		var paymentResp ProcessPaymentResponse
		err = paymentClient.CallJSON(ctx, "process", ProcessPaymentRequest{
			OrderID: orderReq.OrderID,
			Amount:  orderReq.Amount,
		}, &paymentResp, cluster.ToLeader())
		if err != nil {
			return json.Marshal(CreateOrderResponse{
				OrderID: orderReq.OrderID,
				Status:  "failed",
				Message: fmt.Sprintf("payment failed: %v", err),
			})
		}

		// Publish order completed event
		eventData, _ := json.Marshal(map[string]any{
			"order_id":       orderReq.OrderID,
			"transaction_id": paymentResp.TransactionID,
			"status":         "completed",
		})
		platform.Events().PublishFrom(ctx, "order", "order.completed", eventData)

		return json.Marshal(CreateOrderResponse{
			OrderID: orderReq.OrderID,
			Status:  "completed",
			Message: fmt.Sprintf("Payment %s processed", paymentResp.TransactionID),
		})
	})

	platform.Register(orderApp)

	// =========================================================================
	// Create NotificationService (Spread mode - listens to all events)
	// =========================================================================
	notificationApp := cluster.NewApp("notification", cluster.Spread())
	platform.Register(notificationApp)

	// Start platform
	go platform.Run(ctx)

	// Wait for all apps to be ready
	time.Sleep(3 * time.Second)

	// =========================================================================
	// Setup event subscriptions for NotificationService
	// =========================================================================
	sub1, err := platform.Events().Subscribe("order.*", func(e cluster.PlatformEvent) {
		notificationCount.Add(1)
		t.Logf("📧 Notification: Order event received - %s from %s", e.Subject, e.SourceApp)
	})
	require.NoError(t, err)
	defer sub1.Unsubscribe()

	sub2, err := platform.Events().Subscribe("payment.*", func(e cluster.PlatformEvent) {
		notificationCount.Add(1)
		t.Logf("📧 Notification: Payment event received - %s from %s", e.Subject, e.SourceApp)
	})
	require.NoError(t, err)
	defer sub2.Unsubscribe()

	sub3, err := platform.Events().Subscribe("inventory.*", func(e cluster.PlatformEvent) {
		notificationCount.Add(1)
		t.Logf("📧 Notification: Inventory event received - %s from %s", e.Subject, e.SourceApp)
	})
	require.NoError(t, err)
	defer sub3.Unsubscribe()

	// =========================================================================
	// Test 1: Successful order flow
	// =========================================================================
	t.Run("SuccessfulOrderFlow", func(t *testing.T) {
		orderClient := platform.API("order")
		var orderResp CreateOrderResponse
		err := orderClient.CallJSON(ctx, "create", CreateOrderRequest{
			OrderID:   "ORD-001",
			ProductID: "PROD-001",
			Quantity:  5,
			Amount:    99.99,
		}, &orderResp, cluster.ToLeader())

		require.NoError(t, err)
		assert.Equal(t, "ORD-001", orderResp.OrderID)
		assert.Equal(t, "completed", orderResp.Status)
		t.Logf("✅ Order completed: %+v", orderResp)
	})

	// =========================================================================
	// Test 2: Order rejected due to insufficient inventory
	// =========================================================================
	t.Run("InsufficientInventory", func(t *testing.T) {
		orderClient := platform.API("order")
		var orderResp CreateOrderResponse
		err := orderClient.CallJSON(ctx, "create", CreateOrderRequest{
			OrderID:   "ORD-002",
			ProductID: "PROD-003", // Out of stock
			Quantity:  1,
			Amount:    49.99,
		}, &orderResp, cluster.ToLeader())

		require.NoError(t, err)
		assert.Equal(t, "ORD-002", orderResp.OrderID)
		assert.Equal(t, "rejected", orderResp.Status)
		assert.Contains(t, orderResp.Message, "insufficient inventory")
		t.Logf("✅ Order correctly rejected: %+v", orderResp)
	})

	// =========================================================================
	// Test 3: Direct cross-app call to inventory
	// =========================================================================
	t.Run("DirectInventoryCheck", func(t *testing.T) {
		inventoryClient := platform.API("inventory")
		var resp CheckInventoryResponse
		err := inventoryClient.CallJSON(ctx, "check", CheckInventoryRequest{
			ProductID: "PROD-001",
			Quantity:  1,
		}, &resp, cluster.ToLeader())

		require.NoError(t, err)
		assert.True(t, resp.Available)
		t.Logf("✅ Inventory check: stock=%d, available=%v", resp.Stock, resp.Available)
	})

	// =========================================================================
	// Test 4: Multiple concurrent orders
	// =========================================================================
	t.Run("ConcurrentOrders", func(t *testing.T) {
		var wg sync.WaitGroup
		var successCount, rejectCount atomic.Int32

		for i := 0; i < 5; i++ {
			wg.Add(1)
			go func(orderNum int) {
				defer wg.Done()
				orderClient := platform.API("order")
				var orderResp CreateOrderResponse
				err := orderClient.CallJSON(ctx, "create", CreateOrderRequest{
					OrderID:   fmt.Sprintf("CONCURRENT-ORD-%d", orderNum),
					ProductID: "PROD-002", // Limited stock (5)
					Quantity:  2,
					Amount:    29.99,
				}, &orderResp, cluster.ToLeader())

				if err == nil && orderResp.Status == "completed" {
					successCount.Add(1)
				} else {
					rejectCount.Add(1)
				}
			}(i)
		}

		wg.Wait()
		t.Logf("✅ Concurrent orders: %d succeeded, %d rejected", successCount.Load(), rejectCount.Load())
		// With 5 stock and 5 orders of 2 each, at most 2 can succeed
		assert.LessOrEqual(t, successCount.Load(), int32(2))
	})

	// Wait for all events to be processed
	time.Sleep(500 * time.Millisecond)

	// =========================================================================
	// Verify events were received by NotificationService
	// =========================================================================
	t.Run("VerifyEventsPropagated", func(t *testing.T) {
		count := notificationCount.Load()
		t.Logf("✅ Total notifications received: %d", count)
		assert.Greater(t, count, int32(0), "NotificationService should have received events")
	})
}

// =============================================================================
// E2E Test: Platform Lock Coordination Between Apps
// =============================================================================

func TestE2E_CrossAppLockCoordination(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping E2E test in short mode")
	}

	ns := testutil.StartNATS(t)
	defer ns.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	platform, err := cluster.NewPlatform("lock-test", "node-1", ns.URL(),
		cluster.HealthAddr(":38580"),
		cluster.MetricsAddr(":39590"),
	)
	require.NoError(t, err)

	// Shared counter protected by platform lock
	var counter int64
	var counterMu sync.Mutex

	// Create two apps that compete for the same platform lock
	// Use WaitAndAcquire to ensure all increments eventually complete
	app1 := cluster.NewApp("worker-1", cluster.Spread())
	app1.Handle("increment", func(ctx context.Context, req []byte) ([]byte, error) {
		lock := platform.Lock("shared-counter")
		if err := lock.WaitAndAcquire(ctx, 5*time.Second); err != nil {
			return nil, fmt.Errorf("failed to acquire lock: %w", err)
		}
		defer lock.Release(ctx)

		counterMu.Lock()
		counter++
		result := counter
		counterMu.Unlock()

		time.Sleep(20 * time.Millisecond) // Simulate work
		return json.Marshal(map[string]int64{"value": result})
	})
	platform.Register(app1)

	app2 := cluster.NewApp("worker-2", cluster.Spread())
	app2.Handle("increment", func(ctx context.Context, req []byte) ([]byte, error) {
		lock := platform.Lock("shared-counter")
		if err := lock.WaitAndAcquire(ctx, 5*time.Second); err != nil {
			return nil, fmt.Errorf("failed to acquire lock: %w", err)
		}
		defer lock.Release(ctx)

		counterMu.Lock()
		counter++
		result := counter
		counterMu.Unlock()

		time.Sleep(20 * time.Millisecond) // Simulate work
		return json.Marshal(map[string]int64{"value": result})
	})
	platform.Register(app2)

	go platform.Run(ctx)
	time.Sleep(2 * time.Second)

	// Run sequential increments from both apps to avoid timeout issues
	var wg sync.WaitGroup
	incrementsPerApp := 3

	// Run increments sequentially per app but apps run concurrently
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < incrementsPerApp; i++ {
			client := platform.API("worker-1")
			_, err := client.Call(ctx, "increment", nil, cluster.ToAny())
			if err != nil {
				t.Logf("worker-1 increment error: %v", err)
			}
		}
	}()

	go func() {
		defer wg.Done()
		for i := 0; i < incrementsPerApp; i++ {
			client := platform.API("worker-2")
			_, err := client.Call(ctx, "increment", nil, cluster.ToAny())
			if err != nil {
				t.Logf("worker-2 increment error: %v", err)
			}
		}
	}()

	wg.Wait()

	// Verify counter value
	counterMu.Lock()
	finalCount := counter
	counterMu.Unlock()

	t.Logf("✅ Final counter value: %d (expected: %d)", finalCount, incrementsPerApp*2)
	assert.Equal(t, int64(incrementsPerApp*2), finalCount, "Counter should equal total increments")
}

// =============================================================================
// E2E Test: Multi-Node Cross-App Communication
// =============================================================================

func TestE2E_MultiNodeCrossAppCommunication(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping E2E test in short mode")
	}

	ns := testutil.StartNATS(t)
	defer ns.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Create two platforms (simulating two nodes)
	platform1, err := cluster.NewPlatform("multinode", "node-1", ns.URL(),
		cluster.HealthAddr(":38680"),
		cluster.MetricsAddr(":39690"),
		cluster.WithLeaseTTL(2*time.Second),
		cluster.WithHeartbeat(500*time.Millisecond),
	)
	require.NoError(t, err)

	platform2, err := cluster.NewPlatform("multinode", "node-2", ns.URL(),
		cluster.HealthAddr(":38681"),
		cluster.MetricsAddr(":39691"),
		cluster.WithLeaseTTL(2*time.Second),
		cluster.WithHeartbeat(500*time.Millisecond),
	)
	require.NoError(t, err)

	// Register DataService on both nodes (Singleton - only leader responds)
	dataApp1 := cluster.NewApp("data-service", cluster.Singleton())
	dataApp1.Handle("get", func(ctx context.Context, req []byte) ([]byte, error) {
		return json.Marshal(map[string]string{
			"node":  "node-1",
			"value": "data-from-leader",
		})
	})
	platform1.Register(dataApp1)

	dataApp2 := cluster.NewApp("data-service", cluster.Singleton())
	dataApp2.Handle("get", func(ctx context.Context, req []byte) ([]byte, error) {
		return json.Marshal(map[string]string{
			"node":  "node-2",
			"value": "data-from-leader",
		})
	})
	platform2.Register(dataApp2)

	// Register ClientService on node-1 only
	clientApp := cluster.NewApp("client-service", cluster.Spread())
	clientApp.Handle("fetch", func(ctx context.Context, req []byte) ([]byte, error) {
		// Call DataService (should route to leader)
		dataClient := platform1.API("data-service")
		return dataClient.Call(ctx, "get", nil, cluster.ToLeader())
	})
	platform1.Register(clientApp)

	// Start both platforms
	go platform1.Run(ctx)
	go platform2.Run(ctx)

	// Wait for election
	time.Sleep(3 * time.Second)

	// Test cross-app call from ClientService to DataService
	t.Run("CrossAppCallToLeader", func(t *testing.T) {
		clientClient := platform1.API("client-service")
		resp, err := clientClient.Call(ctx, "fetch", nil, cluster.ToAny())
		require.NoError(t, err)

		var result map[string]string
		err = json.Unmarshal(resp, &result)
		require.NoError(t, err)

		t.Logf("✅ Response from data-service leader on %s: %s", result["node"], result["value"])
		assert.Equal(t, "data-from-leader", result["value"])
	})

	// Test platform-wide events across nodes
	// Note: Both platforms share the same NATS server, so events propagate via NATS pub/sub
	t.Run("CrossNodeEvents", func(t *testing.T) {
		var receivedOnNode2 atomic.Bool

		// Subscribe first, then wait for subscription to be active
		sub, err := platform2.Events().Subscribe("test.*", func(e cluster.PlatformEvent) {
			receivedOnNode2.Store(true)
			t.Logf("📧 Node-2 received event: %s from %s", e.Subject, e.SourceNode)
		})
		require.NoError(t, err)
		defer sub.Unsubscribe()

		// Give subscription time to be established
		time.Sleep(200 * time.Millisecond)

		// Publish from node-1
		err = platform1.Events().Publish(ctx, "test.crossnode", []byte("hello from node-1"))
		require.NoError(t, err)

		// Wait for event propagation
		time.Sleep(500 * time.Millisecond)

		assert.True(t, receivedOnNode2.Load(), "Event should be received on node-2")
	})
}

// =============================================================================
// E2E Test: Event-Driven Saga Pattern
// =============================================================================

func TestE2E_EventDrivenSaga(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping E2E test in short mode")
	}

	ns := testutil.StartNATS(t)
	defer ns.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	platform, err := cluster.NewPlatform("saga-test", "node-1", ns.URL(),
		cluster.HealthAddr(":38780"),
		cluster.MetricsAddr(":39790"),
	)
	require.NoError(t, err)

	// Track saga steps
	var sagaSteps sync.Map
	var stepOrder []string
	var stepMu sync.Mutex

	recordStep := func(step string) {
		stepMu.Lock()
		stepOrder = append(stepOrder, step)
		stepMu.Unlock()
		sagaSteps.Store(step, time.Now())
	}

	// Step 1: OrderService creates order and publishes event
	orderApp := cluster.NewApp("order-saga", cluster.Spread())
	orderApp.Handle("start", func(ctx context.Context, req []byte) ([]byte, error) {
		recordStep("1-order-created")
		platform.Events().PublishFrom(ctx, "order-saga", "saga.order.created", []byte(`{"order_id":"SAGA-001"}`))
		return []byte("saga started"), nil
	})
	platform.Register(orderApp)

	// Step 2: InventoryService listens and reserves
	inventoryApp := cluster.NewApp("inventory-saga", cluster.Spread())
	platform.Register(inventoryApp)

	// Step 3: PaymentService listens and processes
	paymentApp := cluster.NewApp("payment-saga", cluster.Spread())
	platform.Register(paymentApp)

	// Step 4: ShippingService listens and ships
	shippingApp := cluster.NewApp("shipping-saga", cluster.Spread())
	platform.Register(shippingApp)

	go platform.Run(ctx)
	time.Sleep(2 * time.Second)

	// Set up event chain
	sub1, _ := platform.Events().Subscribe("saga.order.created", func(e cluster.PlatformEvent) {
		recordStep("2-inventory-reserved")
		platform.Events().PublishFrom(ctx, "inventory-saga", "saga.inventory.reserved", e.Data)
	})
	defer sub1.Unsubscribe()

	sub2, _ := platform.Events().Subscribe("saga.inventory.reserved", func(e cluster.PlatformEvent) {
		recordStep("3-payment-processed")
		platform.Events().PublishFrom(ctx, "payment-saga", "saga.payment.processed", e.Data)
	})
	defer sub2.Unsubscribe()

	sub3, _ := platform.Events().Subscribe("saga.payment.processed", func(e cluster.PlatformEvent) {
		recordStep("4-shipping-initiated")
		platform.Events().PublishFrom(ctx, "shipping-saga", "saga.shipping.initiated", e.Data)
	})
	defer sub3.Unsubscribe()

	sub4, _ := platform.Events().Subscribe("saga.shipping.initiated", func(e cluster.PlatformEvent) {
		recordStep("5-saga-completed")
	})
	defer sub4.Unsubscribe()

	// Start the saga
	orderClient := platform.API("order-saga")
	_, err = orderClient.Call(ctx, "start", nil, cluster.ToAny())
	require.NoError(t, err)

	// Wait for saga to complete
	time.Sleep(1 * time.Second)

	// Verify all steps executed in order
	stepMu.Lock()
	steps := make([]string, len(stepOrder))
	copy(steps, stepOrder)
	stepMu.Unlock()

	t.Logf("✅ Saga steps executed: %v", steps)

	expectedSteps := []string{
		"1-order-created",
		"2-inventory-reserved",
		"3-payment-processed",
		"4-shipping-initiated",
		"5-saga-completed",
	}

	assert.Equal(t, expectedSteps, steps, "Saga should execute steps in order")
}
