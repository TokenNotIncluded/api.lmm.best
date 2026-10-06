package service

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"mime"
	"net"
	"net/mail"
	"net/smtp"
	"strconv"
	"strings"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"gorm.io/gorm"
)

var errMerchantStoreEmail = errors.New("merchant store pickup email delivery failed")

type merchantStorePickupEmail struct {
	destination      string
	tradeNo          string
	pickupURL        string
	verificationCode string
}

func merchantStoreMailAddress(raw string) (*mail.Address, error) {
	if strings.ContainsAny(raw, "\r\n\x00") || len(raw) > 320 {
		return nil, errMerchantStoreEmail
	}
	address, err := mail.ParseAddress(strings.TrimSpace(raw))
	if err != nil || !strings.Contains(address.Address, "@") {
		return nil, errMerchantStoreEmail
	}
	return address, nil
}

func merchantStorePickupEmailMessage(email merchantStorePickupEmail) ([]byte, string, string, error) {
	from := common.SMTPFrom
	if from == "" {
		from = common.SMTPAccount
	}
	sender, err := merchantStoreMailAddress(from)
	if err != nil {
		return nil, "", "", err
	}
	destination, err := merchantStoreMailAddress(email.destination)
	if err != nil || strings.ContainsAny(common.SystemName, "\r\n\x00") {
		return nil, "", "", errMerchantStoreEmail
	}
	subject := "商店订单取货链接"
	content := "你的商店订单已完成支付。\r\n\r\n订单号：" + email.tradeNo + "\r\n取货链接：" + email.pickupURL + "\r\n\r\n请妥善保管取货链接。如商品设置了取件码或登录保护，取货时仍需验证。\r\n平台不会在邮件中发送卡密原文。\r\n"
	if email.verificationCode != "" {
		if len(email.verificationCode) != 6 || strings.Trim(email.verificationCode, "0123456789") != "" || email.pickupURL != "" || email.tradeNo != "" {
			return nil, "", "", errMerchantStoreEmail
		}
		subject = "商店取货邮箱验证"
		content = "商店取货邮箱验证码：" + email.verificationCode + "\r\n\r\n请在发起验证的页面填写此验证码。若不是你本人操作，请忽略此邮件。验证码不包含取货信息。\r\n"
	} else {
		if !merchantStoreTradeNoPattern.MatchString(email.tradeNo) {
			return nil, "", "", errMerchantStoreEmail
		}
		if _, err := merchantStorePublicHTTPSURL(email.pickupURL, false); err != nil {
			return nil, "", "", errMerchantStoreEmail
		}
	}
	messageID := make([]byte, 16)
	if _, err := rand.Read(messageID); err != nil {
		return nil, "", "", errMerchantStoreEmail
	}
	at := strings.LastIndexByte(sender.Address, '@')
	if at < 1 || at == len(sender.Address)-1 {
		return nil, "", "", errMerchantStoreEmail
	}
	encoded := base64.StdEncoding.EncodeToString([]byte(content))
	var body strings.Builder
	for len(encoded) > 76 {
		body.WriteString(encoded[:76] + "\r\n")
		encoded = encoded[76:]
	}
	body.WriteString(encoded + "\r\n")
	headers := []string{
		"From: " + (&mail.Address{Name: common.SystemName, Address: sender.Address}).String(),
		"To: " + destination.String(),
		"Subject: " + mime.QEncoding.Encode("UTF-8", subject),
		"Date: " + time.Now().Format(time.RFC1123Z),
		"Message-ID: <store." + hex.EncodeToString(messageID) + "@" + sender.Address[at+1:] + ">",
		"MIME-Version: 1.0", "Content-Type: text/plain; charset=UTF-8", "Content-Transfer-Encoding: base64",
	}
	return []byte(strings.Join(headers, "\r\n") + "\r\n\r\n" + body.String()), sender.Address, destination.Address, nil
}

// A verification request sends only an ownership code to the current DB
// address. It never contains a bearer pickup link, order data or inventory.
func SendMerchantStoreVerificationEmail(ctx context.Context, userID int, expectedEmail, code string) error {
	if userID <= 0 || len(code) != 6 || strings.Trim(code, "0123456789") != "" {
		return errMerchantStoreEmail
	}
	email, err := model.GetUserEmail(userID)
	if err != nil {
		return errMerchantStoreEmail
	}
	current, currentErr := merchantStoreMailAddress(email)
	expected, expectedErr := merchantStoreMailAddress(expectedEmail)
	if currentErr != nil || expectedErr != nil || current.Address != expected.Address {
		return errMerchantStoreEmail
	}
	return sendMerchantStorePickupEmail(ctx, merchantStorePickupEmail{destination: email, verificationCode: code})
}

// The common mail sender has no connection deadline. This bounded sender
// uses the same administrator-owned SMTP settings/authentication while never
// logging a buyer address, pickup token, SMTP secret or upstream error text.
func sendMerchantStorePickupEmail(ctx context.Context, email merchantStorePickupEmail) error {
	message, sender, destination, err := merchantStorePickupEmailMessage(email)
	if err != nil || common.SMTPServer == "" || common.SMTPPort < 1 || common.SMTPPort > 65535 {
		return errMerchantStoreEmail
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	address := net.JoinHostPort(common.SMTPServer, strconv.Itoa(common.SMTPPort))
	connection, err := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "tcp", address)
	if err != nil {
		return errMerchantStoreEmail
	}
	defer connection.Close()
	baseConnection := connection
	stopCancellation := context.AfterFunc(ctx, func() { _ = baseConnection.Close() })
	defer stopCancellation()
	if deadline, ok := ctx.Deadline(); ok {
		if err := connection.SetDeadline(deadline); err != nil {
			return errMerchantStoreEmail
		}
	}
	tlsConfig := &tls.Config{ServerName: common.SMTPServer, MinVersion: tls.VersionTLS12, InsecureSkipVerify: common.SMTPInsecureSkipVerify} // #nosec G402 -- existing explicit SMTP-only operator setting.
	implicitTLS := common.SMTPSSLEnabled || (common.SMTPPort == 465 && !common.SMTPStartTLSEnabled)
	if implicitTLS {
		secure := tls.Client(connection, tlsConfig)
		if err := secure.HandshakeContext(ctx); err != nil {
			return errMerchantStoreEmail
		}
		connection = secure
	}
	client, err := smtp.NewClient(connection, common.SMTPServer)
	if err != nil {
		return errMerchantStoreEmail
	}
	defer client.Close()
	if common.SMTPStartTLSEnabled && !implicitTLS {
		if supported, _ := client.Extension("STARTTLS"); !supported {
			return errMerchantStoreEmail
		}
		if err := client.StartTLS(tlsConfig); err != nil {
			return errMerchantStoreEmail
		}
	}
	if common.SMTPAccount != "" && common.SMTPToken != "" {
		if err := client.Auth(common.AutoSMTPAuth(common.SMTPAccount, common.SMTPToken)); err != nil {
			return errMerchantStoreEmail
		}
	}
	if err := client.Mail(sender); err != nil {
		return errMerchantStoreEmail
	}
	if err := client.Rcpt(destination); err != nil {
		return errMerchantStoreEmail
	}
	writer, err := client.Data()
	if err != nil {
		return errMerchantStoreEmail
	}
	if _, err := writer.Write(message); err != nil {
		_ = writer.Close()
		return errMerchantStoreEmail
	}
	if err := writer.Close(); err != nil {
		return errMerchantStoreEmail
	}
	// SMTP has accepted the message at DATA success. An ambiguous QUIT must
	// not cause the outbox to resend an already accepted pickup email.
	_ = client.Quit()
	return nil
}

func processMerchantStorePickupEmailBatch(ctx context.Context, limit int, sender func(context.Context, merchantStorePickupEmail) error) (int, error) {
	if limit < 1 || limit > 20 || sender == nil {
		return 0, errMerchantStoreEmail
	}
	processed := 0
	for processed < limit {
		if err := ctx.Err(); err != nil {
			return processed, err
		}
		row, err := model.ClaimMerchantStoreEmailDelivery(time.Now().Unix())
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return processed, nil
		}
		if err != nil {
			return processed, err
		}
		errorCode := ""
		email, err := model.GetMerchantStoreVerifiedEmailAddress(row.BuyerID)
		if errors.Is(err, model.ErrMerchantStoreEmailUnverified) || (err == nil && email == "") {
			if err := model.DeferMerchantStoreEmailVerification(row.ID, row.LeaseToken); err != nil {
				return processed, err
			}
			processed++
			continue
		}
		if err != nil {
			errorCode = "buyer_unavailable"
		} else if _, err := merchantStoreMailAddress(email); err != nil {
			errorCode = "buyer_email_unavailable"
		}
		token, err := model.GetMerchantStoreOrderPickupToken(row.BuyerID, row.OrderID)
		if err != nil {
			errorCode = "pickup_unavailable"
		}
		order, err := model.GetMerchantStorePaymentOrder(row.OrderID)
		if err != nil || order == nil || order.BuyerID != row.BuyerID || order.Status != "paid" || !order.EmailPickupLink {
			errorCode = "order_unavailable"
		}
		origin, err := merchantStorePublicOrigin()
		if err != nil {
			errorCode = "origin_unavailable"
		}
		if errorCode == "" {
			if err := sender(ctx, merchantStorePickupEmail{destination: email, tradeNo: order.TradeNo, pickupURL: origin + "/store/claim/" + token}); err != nil {
				errorCode = "smtp_delivery_failed"
			}
		}
		if errorCode == "" {
			err = model.AckMerchantStoreEmailDelivery(row.ID, row.LeaseToken)
		} else {
			err = model.RetryMerchantStoreEmailDelivery(row.ID, row.LeaseToken, time.Now().Unix(), errorCode)
		}
		if err != nil {
			return processed, err
		}
		processed++
	}
	return processed, nil
}

// RunMerchantStoreWorker is owned by the application's cancellable lifecycle.
// It only releases orders that never issued payment. Sending is opt-in on the
// paid order and uses a durable lease/outbox shared by every backend instance.
func RunMerchantStoreWorker(ctx context.Context) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		if _, err := model.ExpireMerchantStoreOrders(50); err != nil {
			common.SysError("merchant store order cleanup failed")
		}
		if err := reconcileMerchantStorePaymentBatch(ctx, 5); err != nil && ctx.Err() == nil {
			common.SysError("merchant store payment reconciliation failed")
		}
		// Unconfigured SMTP should leave queued emails recoverable instead of
		// consuming all retry attempts before the operator enables mail.
		if common.SMTPServer != "" && (common.SMTPFrom != "" || common.SMTPAccount != "") {
			if _, err := processMerchantStorePickupEmailBatch(ctx, 5, sendMerchantStorePickupEmail); err != nil && ctx.Err() == nil {
				common.SysError("merchant store email outbox processing failed")
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
