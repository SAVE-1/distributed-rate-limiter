package ratelimiter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

const (
	REDISPORT int = 6379
)

func TestPing(t *testing.T) {
	r, err := helperInitForTestContainers(t)

	if err != nil {
		t.Error(err)
		return
	}

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/ping", nil)
	r.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var body map[string]any
	json.Unmarshal(w.Body.Bytes(), &body)
	if body["message"] != "pong" {
		t.Errorf("expected pong, got %v", body["message"])
	}
}

func TestHealth(t *testing.T) {
	r, err := helperInitForTestContainers(t)

	if err != nil {
		t.Error(err)
		return
	}

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/health", nil)
	r.router.ServeHTTP(w, req)

	var body map[string]any
	json.Unmarshal(w.Body.Bytes(), &body)

	// assert specific field values
	if body["health"] != "healthy" {
		t.Errorf("health field wrong: %v", body["health"])
	}
	if body["health-level"] != float64(100) { // JSON numbers are float64 in map[string]any
		t.Errorf("health-level wrong: %v", body["health-level"])
	}
	if body["code"] != float64(239) {
		t.Errorf("code wrong: %v", body["code"])
	}
}


// should be ok, does not reach/use redis related code at any point
func TestRateLimit_MissingFieldsInPayloadJson(t *testing.T) {
	r, err := helperInitForTestContainers(t)

	if err != nil {
		t.Error(err)
		return
	}

	/*
		len() 		 = 						 5
		"Passes" 	 = interface {}(bool) 	 false
		"HitCount" 	 = interface {}(float64) 3
		"FirstHit" 	 = interface {}(float64) 1787733908
		"Remaining"	 = interface {}(float64) 0
		"ResetsUnix" = interface {}(float64) 0
	*/

	var body1 map[string]any
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/ratelimit",
	bytes.NewBufferString(`{"ClientId": "user1" }`))
	req.Header.Set("Content-Type", "application/json")

	r.router.ServeHTTP(w, req)
	json.Unmarshal(w.Body.Bytes(), &body1)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}

	var body map[string]any
	json.Unmarshal(w.Body.Bytes(), &body)
	if _, ok := body["error"]; !ok {
		t.Error("expected error field in response")
	}
}

func TestRateLimiter_LimitExceeded(t *testing.T) {
	r, err := helperInitForTestContainers(t)

	if err != nil {
		t.Error(err)
		return
	}

	/*
		len() 		 = 						 5
		"Passes" 	 = interface {}(bool) 	 false
		"HitCount" 	 = interface {}(float64) 3
		"FirstHit" 	 = interface {}(float64) 1787733908
		"Remaining"	 = interface {}(float64) 0
		"ResetsUnix" = interface {}(float64) 0
	*/

	var body1 map[string]any
	w1 := httptest.NewRecorder()
	req1 := httptest.NewRequest(http.MethodPost, "/v1/ratelimit",
	bytes.NewBufferString(`{"ClientId": "user1", "RulesId": "content name", "Algorithm": "fixed_window" }`))
	req1.Header.Set("Content-Type", "application/json")
	
	var body2 map[string]any
	w2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodPost, "/v1/ratelimit",
	bytes.NewBufferString(`{"ClientId": "user1", "RulesId": "content name", "Algorithm": "fixed_window" }`))
	req2.Header.Set("Content-Type", "application/json")
	
	var body3 map[string]any
	w3 := httptest.NewRecorder()
	req3 := httptest.NewRequest(http.MethodPost, "/v1/ratelimit",
	bytes.NewBufferString(`{"ClientId": "user1", "RulesId": "content name", "Algorithm": "fixed_window" }`))

	r.router.ServeHTTP(w1, req1)
	json.Unmarshal(w1.Body.Bytes(), &body1)

	if v, ok := body1["Passes"]; !ok && v != true {
		t.Error("expected 'Passes' = true in response")
	}
	
	r.router.ServeHTTP(w2, req2)
	json.Unmarshal(w2.Body.Bytes(), &body2)

	if v, ok := body2["Passes"]; !ok && v != false {
		t.Error("expected 'Passes' = false in response")
	}

	r.router.ServeHTTP(w3, req3)
	json.Unmarshal(w3.Body.Bytes(), &body3)

	if v, ok := body3["Passes"]; !ok && v != false {
		t.Error("expected 'Passes' = false in response")
	}

	if v, ok := body3["HitCount"]; !ok && v != 3 {
		t.Error("expected 'HitCount' = 3 in response")
	}

	if v, ok := body3["Remaining"]; !ok && v != 0 {
		t.Error("expected 'Remaining' = 0 in response")
	}

}

func helperInitForTestContainers(t *testing.T) (*RatelimiterHandler, error) {
	// code from: https://golang.testcontainers.org/quickstart/
	ctx := context.Background()
	redisC, err := testcontainers.Run(
		ctx, "redis:latest",
		testcontainers.WithExposedPorts(fmt.Sprintf("%d/tcp", REDISPORT)),
		testcontainers.WithWaitStrategy(
			wait.ForListeningPort(fmt.Sprintf("%d/tcp", REDISPORT)),
			wait.ForLog("Ready to accept connections"),
		),
	)
	testcontainers.CleanupContainer(t, redisC)
	require.NoError(t, err)

	redisEndpoint, err := redisC.Endpoint(ctx, fmt.Sprintf("%d/tcp", REDISPORT))
	require.NoError(t, err)

	tt := strings.LastIndex(redisEndpoint, ":")

	config := RateLimiterConfiguration{
		RedisAddress:             "127.0.0.1:" + redisEndpoint[tt+1:],
		RedisUsername:            "",
		RedisPassword:            "",
		Period:                   time.Minute,
		Limit:                    2,
		AllowStartupWithoutRedis: false,
		Port:                     12600,
		Mode:                     "dev",
	}

	r, err := NewRatelimiter(config)

	if err != nil {
		t.Error("error in myInit()")
		return nil, err
	}

	return r, nil
}


