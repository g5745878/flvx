package contract_test

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"go-backend/internal/auth"
	"go-backend/internal/http/response"
	"go-backend/internal/store/repo"
)

// TestSpeedLimitWithoutTunnelContract tests that speed limits can be created without binding to a tunnel
func TestSpeedLimitWithoutTunnelContract(t *testing.T) {
	secret := "contract-jwt-secret"
	router, _ := setupContractRouter(t, secret)

	adminToken, err := auth.GenerateToken(1, "admin_user", 0, secret)
	if err != nil {
		t.Fatalf("generate admin token: %v", err)
	}

	// Create a speed limit without tunnel binding
	t.Run("create speed limit without tunnel", func(t *testing.T) {
		body := `{"name":"test-limit-no-tunnel","speed":100,"status":1}`
		req := httptest.NewRequest(http.MethodPost, "/api/v1/speed-limit/create", bytes.NewBufferString(body))
		req.Header.Set("Authorization", adminToken)
		req.Header.Set("Content-Type", "application/json")
		res := httptest.NewRecorder()
		router.ServeHTTP(res, req)

		assertCode(t, res, 0)
	})

	// Verify the speed limit has null tunnelId
	t.Run("list speed limits shows null tunnelId", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/speed-limit/list", nil)
		req.Header.Set("Authorization", adminToken)
		res := httptest.NewRecorder()
		router.ServeHTTP(res, req)

		var out response.R
		if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if out.Code != 0 {
			t.Fatalf("expected code 0, got %d", out.Code)
		}

		data, ok := out.Data.([]interface{})
		if !ok {
			t.Fatalf("expected data to be array, got %T", out.Data)
		}

		// Find our speed limit
		var found bool
		for _, item := range data {
			m, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			if m["name"] == "test-limit-no-tunnel" {
				found = true
				// tunnelId should be nil/not present for unbound speed limits
				if tunnelID, exists := m["tunnelId"]; exists && tunnelID != nil {
					t.Fatalf("expected tunnelId to be nil for unbound speed limit, got %v", tunnelID)
				}
				break
			}
		}

		if !found {
			t.Fatal("speed limit 'test-limit-no-tunnel' not found in list")
		}
	})
}

// TestSpeedLimitWithTunnelContract tests that speed limits can still be bound to tunnels
func TestSpeedLimitWithTunnelContract(t *testing.T) {
	secret := "contract-jwt-secret"
	router, r := setupContractRouter(t, secret)

	adminToken, err := auth.GenerateToken(1, "admin_user", 0, secret)
	if err != nil {
		t.Fatalf("generate admin token: %v", err)
	}

	// First create a tunnel
	tunnelID := mustCreateSpeedLimitTunnel(t, r, "test-tunnel-for-limit")

	// Create a speed limit with tunnel binding
	t.Run("create speed limit with tunnel", func(t *testing.T) {
		body := `{"name":"test-limit-with-tunnel","speed":200,"tunnelId":` + jsonInt(tunnelID) + `,"status":1}`
		req := httptest.NewRequest(http.MethodPost, "/api/v1/speed-limit/create", bytes.NewBufferString(body))
		req.Header.Set("Authorization", adminToken)
		req.Header.Set("Content-Type", "application/json")
		res := httptest.NewRecorder()
		router.ServeHTTP(res, req)

		assertCode(t, res, 0)
	})

	// Verify the speed limit has the tunnelId
	t.Run("list speed limits shows tunnelId", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/speed-limit/list", nil)
		req.Header.Set("Authorization", adminToken)
		res := httptest.NewRecorder()
		router.ServeHTTP(res, req)

		var out response.R
		if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if out.Code != 0 {
			t.Fatalf("expected code 0, got %d", out.Code)
		}

		data, ok := out.Data.([]interface{})
		if !ok {
			t.Fatalf("expected data to be array, got %T", out.Data)
		}

		var found bool
		for _, item := range data {
			m, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			if m["name"] == "test-limit-with-tunnel" {
				found = true
				tunnelIDVal, exists := m["tunnelId"]
				if !exists || tunnelIDVal == nil {
					t.Fatal("expected tunnelId to be present for bound speed limit")
				}
				// Verify tunnelId matches
				if tunnelIDFloat, ok := tunnelIDVal.(float64); ok {
					if int64(tunnelIDFloat) != tunnelID {
						t.Fatalf("expected tunnelId %d, got %d", tunnelID, int64(tunnelIDFloat))
					}
				}
				break
			}
		}

		if !found {
			t.Fatal("speed limit 'test-limit-with-tunnel' not found in list")
		}
	})
}

// TestSpeedLimitUpdateTunnelBindingContract tests updating speed limit tunnel binding
func TestSpeedLimitUpdateTunnelBindingContract(t *testing.T) {
	secret := "contract-jwt-secret"
	router, r := setupContractRouter(t, secret)

	adminToken, err := auth.GenerateToken(1, "admin_user", 0, secret)
	if err != nil {
		t.Fatalf("generate admin token: %v", err)
	}

	// Create a tunnel
	tunnelID := mustCreateSpeedLimitTunnel(t, r, "test-tunnel-update")

	// Create a speed limit without tunnel
	speedLimitID := mustCreateSpeedLimitRepo(t, r, "test-limit-update", 0)

	// Update to bind to tunnel
	t.Run("update speed limit to bind tunnel", func(t *testing.T) {
		body := `{"id":` + jsonInt(speedLimitID) + `,"name":"test-limit-update","speed":150,"tunnelId":` + jsonInt(tunnelID) + `,"status":1}`
		req := httptest.NewRequest(http.MethodPost, "/api/v1/speed-limit/update", bytes.NewBufferString(body))
		req.Header.Set("Authorization", adminToken)
		req.Header.Set("Content-Type", "application/json")
		res := httptest.NewRecorder()
		router.ServeHTTP(res, req)

		assertCode(t, res, 0)
	})

	// Verify binding
	t.Run("verify tunnel binding after update", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/speed-limit/list", nil)
		req.Header.Set("Authorization", adminToken)
		res := httptest.NewRecorder()
		router.ServeHTTP(res, req)

		var out response.R
		if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if out.Code != 0 {
			t.Fatalf("expected code 0, got %d", out.Code)
		}

		data, ok := out.Data.([]interface{})
		if !ok {
			t.Fatalf("expected data to be array, got %T", out.Data)
		}

		for _, item := range data {
			m, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			if m["name"] == "test-limit-update" {
				tunnelIDVal, exists := m["tunnelId"]
				if !exists || tunnelIDVal == nil {
					t.Fatal("expected tunnelId to be present after update")
				}
				return
			}
		}
		t.Fatal("speed limit 'test-limit-update' not found")
	})

	// Update to unbind from tunnel (set tunnelId to null)
	t.Run("update speed limit to unbind tunnel", func(t *testing.T) {
		body := `{"id":` + jsonInt(speedLimitID) + `,"name":"test-limit-update","speed":150,"status":1}`
		req := httptest.NewRequest(http.MethodPost, "/api/v1/speed-limit/update", bytes.NewBufferString(body))
		req.Header.Set("Authorization", adminToken)
		req.Header.Set("Content-Type", "application/json")
		res := httptest.NewRecorder()
		router.ServeHTTP(res, req)

		assertCode(t, res, 0)
	})

	// Verify unbinding
	t.Run("verify tunnel unbinding after update", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/speed-limit/list", nil)
		req.Header.Set("Authorization", adminToken)
		res := httptest.NewRecorder()
		router.ServeHTTP(res, req)

		var out response.R
		if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if out.Code != 0 {
			t.Fatalf("expected code 0, got %d", out.Code)
		}

		data, ok := out.Data.([]interface{})
		if !ok {
			t.Fatalf("expected data to be array, got %T", out.Data)
		}

		for _, item := range data {
			m, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			if m["name"] == "test-limit-update" {
				if tunnelIDVal, exists := m["tunnelId"]; exists && tunnelIDVal != nil {
					t.Fatalf("expected tunnelId to be nil after unbinding, got %v", tunnelIDVal)
				}
				return
			}
		}
		t.Fatal("speed limit 'test-limit-update' not found")
	})
}

// TestSpeedLimitDatabaseNullableFields tests database-level nullable fields
func TestSpeedLimitDatabaseNullableFields(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "speed-limit-null.db")
	r, err := repo.Open(dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = r.Close() })

	// Create speed limit via repository
	t.Run("repository create speed limit without tunnel", func(t *testing.T) {
		id, err := r.CreateSpeedLimit("db-test-limit", 100, nil, "", 1, 1)
		if err != nil {
			t.Fatalf("CreateSpeedLimit failed: %v", err)
		}
		if id <= 0 {
			t.Fatalf("expected valid id, got %d", id)
		}
	})

	// Verify TunnelID is null in database
	t.Run("verify null TunnelID in database", func(t *testing.T) {
		var tunnelID sql.NullInt64
		var tunnelName sql.NullString
		err := r.DB().Raw("SELECT tunnel_id, tunnel_name FROM speed_limit WHERE name = ?", "db-test-limit").Row().Scan(&tunnelID, &tunnelName)
		if err != nil {
			t.Fatalf("query failed: %v", err)
		}
		if tunnelID.Valid {
			t.Fatalf("expected TunnelID to be NULL, got %d", tunnelID.Int64)
		}
		if tunnelName.Valid && tunnelName.String != "" {
			t.Fatalf("expected TunnelName to be NULL or empty, got %s", tunnelName.String)
		}
	})

	// Create a tunnel for binding test
	tunnelID := mustCreateSpeedLimitTunnel(t, r, "db-test-tunnel")

	// Create speed limit with tunnel
	t.Run("repository create speed limit with tunnel", func(t *testing.T) {
		id, err := r.CreateSpeedLimit("db-test-limit-with-tunnel", 200, &tunnelID, "db-test-tunnel", 1, 1)
		if err != nil {
			t.Fatalf("CreateSpeedLimit failed: %v", err)
		}
		if id <= 0 {
			t.Fatalf("expected valid id, got %d", id)
		}
	})

	// Verify TunnelID is set
	t.Run("verify TunnelID is set in database", func(t *testing.T) {
		var dbTunnelID sql.NullInt64
		var dbTunnelName sql.NullString
		err := r.DB().Raw("SELECT tunnel_id, tunnel_name FROM speed_limit WHERE name = ?", "db-test-limit-with-tunnel").Row().Scan(&dbTunnelID, &dbTunnelName)
		if err != nil {
			t.Fatalf("query failed: %v", err)
		}
		if !dbTunnelID.Valid {
			t.Fatal("expected TunnelID to be valid")
		}
		if dbTunnelID.Int64 != tunnelID {
			t.Fatalf("expected TunnelID %d, got %d", tunnelID, dbTunnelID.Int64)
		}
		if !dbTunnelName.Valid || dbTunnelName.String != "db-test-tunnel" {
			t.Fatalf("expected TunnelName 'db-test-tunnel', got %v", dbTunnelName.String)
		}
	})

	// Test GetSpeedLimitTunnelID returns correct nullability
	t.Run("GetSpeedLimitTunnelID returns null for unbound limit", func(t *testing.T) {
		result := r.GetSpeedLimitTunnelID(1) // First speed limit (db-test-limit)
		if result.Valid {
			t.Fatalf("expected GetSpeedLimitTunnelID to return invalid/null, got valid with value %d", result.Int64)
		}
	})

	t.Run("GetSpeedLimitTunnelID returns value for bound limit", func(t *testing.T) {
		result := r.GetSpeedLimitTunnelID(2) // Second speed limit (db-test-limit-with-tunnel)
		if !result.Valid {
			t.Fatal("expected GetSpeedLimitTunnelID to return valid result for bound limit")
		}
		if result.Int64 != tunnelID {
			t.Fatalf("expected TunnelID %d, got %d", tunnelID, result.Int64)
		}
	})
}

// TestSpeedLimitUpdateUnbindFromTunnel tests unbinding a speed limit from a tunnel
func TestSpeedLimitUpdateUnbindFromTunnel(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "speed-limit-unbind.db")
	r, err := repo.Open(dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = r.Close() })

	// Create tunnel
	tunnelID := mustCreateSpeedLimitTunnel(t, r, "unbind-test-tunnel")

	// Create speed limit bound to tunnel
	speedLimitID, err := r.CreateSpeedLimit("unbind-test-limit", 300, &tunnelID, "unbind-test-tunnel", 1, 1)
	if err != nil {
		t.Fatalf("create speed limit: %v", err)
	}

	// Verify initial binding
	t.Run("verify initial binding", func(t *testing.T) {
		result := r.GetSpeedLimitTunnelID(speedLimitID)
		if !result.Valid {
			t.Fatal("expected initial binding to tunnel")
		}
		if result.Int64 != tunnelID {
			t.Fatalf("expected tunnel ID %d, got %d", tunnelID, result.Int64)
		}
	})

	// Update to unbind
	t.Run("unbind speed limit from tunnel via UpdateSpeedLimit", func(t *testing.T) {
		err := r.UpdateSpeedLimit(speedLimitID, "unbind-test-limit", 300, nil, "", 1, time.Now().UnixMilli())
		if err != nil {
			t.Fatalf("UpdateSpeedLimit failed: %v", err)
		}
	})

	// Verify unbinding
	t.Run("verify unbinding after update", func(t *testing.T) {
		result := r.GetSpeedLimitTunnelID(speedLimitID)
		if result.Valid {
			t.Fatalf("expected GetSpeedLimitTunnelID to return invalid/null after unbind, got valid with value %d", result.Int64)
		}
	})
}

// TestSpeedLimitGetSpeed tests the GetSpeedLimitSpeed function
func TestSpeedLimitGetSpeed(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "speed-limit-getspeed.db")
	r, err := repo.Open(dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = r.Close() })

	// Create speed limit
	speedLimitID, err := r.CreateSpeedLimit("get-speed-test", 500, nil, "", 1, 1)
	if err != nil {
		t.Fatalf("create speed limit: %v", err)
	}

	// Test GetSpeedLimitSpeed
	t.Run("GetSpeedLimitSpeed returns correct speed", func(t *testing.T) {
		speed, err := r.GetSpeedLimitSpeed(speedLimitID)
		if err != nil {
			t.Fatalf("GetSpeedLimitSpeed failed: %v", err)
		}
		if speed != 500 {
			t.Fatalf("expected speed 500, got %d", speed)
		}
	})

	t.Run("GetSpeedLimitSpeed returns error for non-existent id", func(t *testing.T) {
		_, err := r.GetSpeedLimitSpeed(99999)
		if err == nil {
			t.Fatal("expected error for non-existent speed limit ID")
		}
	})
}

// Helper functions

func mustCreateSpeedLimitTunnel(t *testing.T, r *repo.Repository, name string) int64 {
	t.Helper()
	now := time.Now().UnixMilli()
	if err := r.DB().Exec(`
		INSERT INTO tunnel(name, traffic_ratio, type, protocol, flow, created_time, updated_time, status, in_ip, inx)
		VALUES(?, 1.0, 1, 'tls', 99999, ?, ?, 1, NULL, 0)
	`, name, now, now).Error; err != nil {
		t.Fatalf("create tunnel failed: %v", err)
	}
	return mustLastInsertID(t, r, name)
}

func mustCreateSpeedLimitRepo(t *testing.T, r *repo.Repository, name string, tunnelID int64) int64 {
	t.Helper()
	now := time.Now().UnixMilli()
	var tid *int64
	if tunnelID > 0 {
		tid = &tunnelID
	}
	id, err := r.CreateSpeedLimit(name, 100, tid, "", now, 1)
	if err != nil {
		t.Fatalf("create speed limit failed: %v", err)
	}
	return id
}
