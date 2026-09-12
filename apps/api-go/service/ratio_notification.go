package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
)

// Mandatory public-IP policy independent of configurable provider-fetch policy.
// Resolve and dial the same address; no proxies, redirects or worker bypass.
func ratioPublicIP(ip net.IP) bool {
	if ip == nil || !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() || ratioSpecialIP(ip) {
		return false
	}
	if ip.To4() == nil {
		_, public, _ := net.ParseCIDR("2000::/3")
		return public.Contains(ip)
	}
	return true
}
func ratioSpecialIP(ip net.IP) bool {
	for _, cidr := range []string{"100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4", "2001:db8::/32", "64:ff9b::/96", "64:ff9b:1::/48", "2002::/16", "2001::/23", "3fff::/20"} {
		_, block, _ := net.ParseCIDR(cidr)
		if block.Contains(ip) {
			return true
		}
	}
	return false
}
func ratioWebhookClient() *http.Client {
	return &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }, Transport: &http.Transport{
		TLSHandshakeTimeout: 5 * time.Second, DisableKeepAlives: true,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, err
			}
			ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
			if err != nil {
				return nil, fmt.Errorf("DNS resolution failed")
			}
			if len(ips) == 0 {
				return nil, fmt.Errorf("no public address")
			}
			for _, ip := range ips {
				if !ratioPublicIP(ip.IP) {
					return nil, fmt.Errorf("non-public address rejected")
				}
			}
			return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, network, net.JoinHostPort(ips[0].IP.String(), port))
		},
	}}
}
func sendRatioWebhook(ctx context.Context, target, secret string, payload []byte, eventID string) error {
	u, err := url.Parse(target)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Fragment != "" || (u.Port() != "" && u.Port() != "443") {
		return fmt.Errorf("webhook requires HTTPS on port 443 without credentials or fragment")
	}
	if secret == "" {
		return fmt.Errorf("webhook signing secret required")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("invalid webhook request")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Webhook-Signature", generateSignature(secret, payload))
	req.Header.Set("X-Webhook-Event-ID", eventID)
	resp, err := ratioWebhookClient().Do(req)
	if err != nil {
		return fmt.Errorf("webhook transport failed (DNS, public-IP policy, TLS or timeout)")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("webhook HTTP status %d", resp.StatusCode)
	}
	return nil
}

func dispatchRatioDelivery(ctx context.Context, d model.RatioDelivery) error {
	now := time.Now().Unix()
	// The claim itself consumes an attempt, so repeated process crashes are bounded.
	claim := model.DB.Model(&model.RatioDelivery{}).Where("id = ? AND next_at <= ? AND status IN ? AND attempts = ? AND attempts < 5", d.ID, now, []string{"pending", "sending"}, d.Attempts).Updates(map[string]any{"status": "sending", "attempts": d.Attempts + 1, "next_at": now + 60})
	if claim.Error != nil {
		return claim.Error
	}
	if claim.RowsAffected == 0 {
		return nil
	}
	var user model.User
	var event model.RatioNotification
	err := model.DB.First(&user, d.UserID).Error
	if err == nil {
		err = model.DB.First(&event, "id = ?", d.EventID).Error
	}
	var changes []model.RatioChange
	if err == nil {
		changes, err = model.VisibleRatioChanges(event, user)
	}
	status := "delivered"
	s := user.GetSetting()
	if err == nil && (len(changes) == 0 || s.NotifyType != "webhook" || s.WebhookUrl == "") {
		status = "skipped"
	} else if err == nil {
		payload, e := common.MarshalLimit(map[string]any{"type": "ratio.changed", "event_id": event.ID, "effective_at": event.EffectiveAt, "changes": changes}, webhookPayloadMaxBytes)
		err = e
		if err == nil {
			err = sendRatioWebhook(ctx, s.WebhookUrl, s.WebhookSecret, payload, event.ID)
		}
	}
	last := ""
	if err != nil {
		status = "pending"
		last = "recipient or event unavailable"
		if json.Valid([]byte(event.Changes)) {
			last = err.Error()
		}
		if len(last) > 200 {
			last = last[:200]
		}
		if d.Attempts+1 >= 5 {
			status = "failed"
		}
	}
	return model.DB.Model(&model.RatioDelivery{}).Where("id = ? AND status = ? AND attempts = ?", d.ID, "sending", d.Attempts+1).Updates(map[string]any{"status": status, "last_error": last, "next_at": now + int64(30*(1<<d.Attempts))}).Error
}

func RunRatioNotifications(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			var events []model.RatioNotification
			if err := model.DB.Where("expanded = ?", false).Order("effective_at").Limit(10).Find(&events).Error; err != nil {
				common.SysLog("ratio notification outbox read failed")
				continue
			}
			for _, e := range events {
				if err := model.ExpandRatioNotification(e); err != nil {
					common.SysLog("ratio notification fanout failed")
				}
			}
			model.DB.Model(&model.RatioDelivery{}).Where("status = ? AND attempts >= 5 AND next_at <= ?", "sending", time.Now().Unix()).Updates(map[string]any{"status": "failed", "last_error": "delivery lease expired"})
			var deliveries []model.RatioDelivery
			if err := model.DB.Where("status IN ? AND next_at <= ? AND attempts < 5", []string{"pending", "sending"}, time.Now().Unix()).Order("id").Limit(20).Find(&deliveries).Error; err != nil {
				continue
			}
			for _, d := range deliveries {
				if ctx.Err() != nil {
					return
				}
				if err := dispatchRatioDelivery(ctx, d); err != nil {
					common.SysLog("ratio notification delivery state write failed")
				}
			}
		}
	}
}
