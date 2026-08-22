package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/pkg/response"
)

// ── Billing handlers ──

func (s *Server) handleBillingBalance(w http.ResponseWriter, r *http.Request) {
	response.OK(w, map[string]interface{}{"balance": 0, "currency": "points"})
}

func (s *Server) handleBillingPlans(w http.ResponseWriter, r *http.Request) {
	response.OK(w, map[string]interface{}{"plans": []interface{}{}})
}

func (s *Server) handleBillingSubscribe(w http.ResponseWriter, r *http.Request) {
	response.OK(w, map[string]interface{}{"success": false, "message": "not available"})
}

func (s *Server) handleBillingSubscription(w http.ResponseWriter, r *http.Request) {
	response.OK(w, map[string]interface{}{"subscription": nil})
}

func (s *Server) handleBillingConsumption(w http.ResponseWriter, r *http.Request) {
	response.OK(w, map[string]interface{}{"records": []interface{}{}})
}

func (s *Server) handleBillingConsumptionSummary(w http.ResponseWriter, r *http.Request) {
	response.OK(w, map[string]interface{}{"summary": nil})
}

func (s *Server) handleBillingRecharge(w http.ResponseWriter, r *http.Request) {
	response.OK(w, map[string]interface{}{"success": false, "message": "not available"})
}

func (s *Server) handleBillingRechargeOrders(w http.ResponseWriter, r *http.Request) {
	response.OK(w, map[string]interface{}{"orders": []interface{}{}})
}

func (s *Server) handleBillingRedeem(w http.ResponseWriter, r *http.Request) {
	response.OK(w, map[string]interface{}{"success": false, "message": "not available"})
}

func (s *Server) handleBillingFeatures(w http.ResponseWriter, r *http.Request) {
	response.OK(w, map[string]interface{}{"features": map[string]interface{}{
		"billing_enabled": false,
	}})
}

func (s *Server) handleAdminBillingOverview(w http.ResponseWriter, r *http.Request) {
	response.OK(w, map[string]interface{}{"overview": nil})
}

func (s *Server) handleAdminBillingUsers(w http.ResponseWriter, r *http.Request) {
	response.OK(w, map[string]interface{}{"users": []interface{}{}})
}

func (s *Server) handleAdminBillingUserDetail(w http.ResponseWriter, r *http.Request) {
	response.OK(w, map[string]interface{}{"detail": nil})
}

func (s *Server) handleAdminBillingRevenue(w http.ResponseWriter, r *http.Request) {
	response.OK(w, map[string]interface{}{"revenue": []interface{}{}})
}

func (s *Server) handleAdminBillingConsumption(w http.ResponseWriter, r *http.Request) {
	response.OK(w, map[string]interface{}{"consumption": []interface{}{}})
}

func (s *Server) handleAdminBillingPointRates(w http.ResponseWriter, r *http.Request) {
	response.OK(w, map[string]interface{}{"rates": []interface{}{}})
}

func (s *Server) handleAdminBillingCreatePointRate(w http.ResponseWriter, r *http.Request) {
	response.OK(w, map[string]interface{}{"success": false})
}

func (s *Server) handleAdminBillingUpdatePointRate(w http.ResponseWriter, r *http.Request) {
	response.OK(w, map[string]interface{}{"success": false})
}

func (s *Server) handleAdminBillingDeletePointRate(w http.ResponseWriter, r *http.Request) {
	response.OK(w, map[string]interface{}{"success": false})
}

func (s *Server) handleAdminBillingSetMultiplier(w http.ResponseWriter, r *http.Request) {
	response.OK(w, map[string]interface{}{"success": false})
}

func (s *Server) handleAdminBillingPlans(w http.ResponseWriter, r *http.Request) {
	response.OK(w, map[string]interface{}{"plans": []interface{}{}})
}

func (s *Server) handleAdminBillingUpdatePlan(w http.ResponseWriter, r *http.Request) {
	response.OK(w, map[string]interface{}{"success": false})
}

func (s *Server) handleAdminBillingRecharge(w http.ResponseWriter, r *http.Request) {
	response.OK(w, map[string]interface{}{"success": false})
}

func (s *Server) handleAdminBillingCreateRedeemCodes(w http.ResponseWriter, r *http.Request) {
	response.OK(w, map[string]interface{}{"success": false})
}

func (s *Server) handleAdminBillingListRedeemCodes(w http.ResponseWriter, r *http.Request) {
	response.OK(w, map[string]interface{}{"codes": []interface{}{}})
}

func (s *Server) handleAdminBillingDisableRedeemCode(w http.ResponseWriter, r *http.Request) {
	response.OK(w, map[string]interface{}{"success": false})
}

// ── Billing logic ──

func (s *Server) SettleWritingPoints(ctx context.Context, userID, traceID, modelName, taskType string, promptTokens, completionTokens int) (float64, error) {
	return 0, nil
}

func (s *Server) CheckUserBalance(ctx context.Context, userID string, minRequired float64) (float64, bool, error) {
	return 0, true, nil
}

// ── Material folder handlers ──

func (s *Server) handleFolderList(w http.ResponseWriter, r *http.Request) {
	response.OK(w, map[string]interface{}{"folders": []interface{}{}})
}

func (s *Server) handleFolderCreate(w http.ResponseWriter, r *http.Request) {
	response.OK(w, map[string]interface{}{"success": false})
}

func (s *Server) handleFolderUpdate(w http.ResponseWriter, r *http.Request) {
	response.OK(w, map[string]interface{}{"success": false})
}

func (s *Server) handleFolderDelete(w http.ResponseWriter, r *http.Request) {
	response.OK(w, map[string]interface{}{"success": false})
}

func (s *Server) handleMaterialMove(w http.ResponseWriter, r *http.Request) {
	response.OK(w, map[string]interface{}{"success": false})
}

func (s *Server) InitNewUserBalance(ctx context.Context, userID string) {}

// ── Session management ──

func generateSessionID() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return time.Now().Format("20060102150405000000")
	}
	return hex.EncodeToString(buf)
}

func (s *Server) recordSession(r *http.Request, userID, jti string) {}

func (s *Server) updateSessionActivity(jti string) {}

func (s *Server) isSessionRevoked(jti string) bool { return false }

// ── Session management handlers ──

func (s *Server) handleListUserActiveSessions(w http.ResponseWriter, r *http.Request) {
	response.OK(w, map[string]interface{}{"sessions": []interface{}{}})
}
