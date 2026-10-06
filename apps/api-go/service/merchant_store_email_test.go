package service

import (
	"bufio"
	"context"
	"encoding/base64"
	"io"
	"mime"
	"mime/multipart"
	"net"
	"net/mail"
	"strings"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/stretchr/testify/require"
)

func merchantStoreEmailTestBodies(t *testing.T, raw []byte) map[string]string {
	t.Helper()
	message, err := mail.ReadMessage(strings.NewReader(string(raw)))
	require.NoError(t, err)
	kind, params, err := mime.ParseMediaType(message.Header.Get("Content-Type"))
	require.NoError(t, err)
	bodies := make(map[string]string)
	if kind == "multipart/alternative" {
		reader := multipart.NewReader(message.Body, params["boundary"])
		for {
			part, err := reader.NextPart()
			if err == io.EOF {
				break
			}
			require.NoError(t, err)
			partKind, _, err := mime.ParseMediaType(part.Header.Get("Content-Type"))
			require.NoError(t, err)
			require.Equal(t, "base64", part.Header.Get("Content-Transfer-Encoding"))
			body, err := io.ReadAll(base64.NewDecoder(base64.StdEncoding, part))
			require.NoError(t, err)
			bodies[partKind] = string(body)
		}
	} else {
		body, err := io.ReadAll(base64.NewDecoder(base64.StdEncoding, message.Body))
		require.NoError(t, err)
		bodies[kind] = string(body)
	}
	return bodies
}

func merchantStoreSMTPTestSettings(t *testing.T) {
	t.Helper()
	oldServer, oldPort, oldFrom, oldAccount, oldToken := common.SMTPServer, common.SMTPPort, common.SMTPFrom, common.SMTPAccount, common.SMTPToken
	oldSSL, oldStart := common.SMTPSSLEnabled, common.SMTPStartTLSEnabled
	common.SMTPServer = "127.0.0.1"
	common.SMTPFrom = "sender@example.com"
	common.SMTPAccount = ""
	common.SMTPToken = ""
	common.SMTPSSLEnabled = false
	common.SMTPStartTLSEnabled = false
	t.Cleanup(func() {
		common.SMTPServer = oldServer
		common.SMTPPort = oldPort
		common.SMTPFrom = oldFrom
		common.SMTPAccount = oldAccount
		common.SMTPToken = oldToken
		common.SMTPSSLEnabled = oldSSL
		common.SMTPStartTLSEnabled = oldStart
	})
}

func TestMerchantStorePickupEmailRejectsHeaderInjectionAndSeparatesVerification(t *testing.T) {
	merchantStoreSMTPTestSettings(t)
	email := merchantStorePickupEmail{destination: "buyer@example.com", tradeNo: "MS" + strings.Repeat("a", 30), pickupURL: "https://api.example.com/store/claim/" + strings.Repeat("a", 43)}
	message, _, _, err := merchantStorePickupEmailMessage(email)
	require.NoError(t, err)
	bodies := merchantStoreEmailTestBodies(t, message)
	require.Contains(t, bodies["text/plain"], email.pickupURL)
	require.Contains(t, bodies["text/html"], email.pickupURL)
	require.NotContains(t, bodies["text/plain"], "PRIVATE-CARD")
	for _, bad := range []string{"buyer@example.com\r\nBcc: other@example.com", "not-an-address", "buyer@example.com\x00"} {
		email.destination = bad
		_, _, _, err := merchantStorePickupEmailMessage(email)
		require.Error(t, err)
	}
	email = merchantStorePickupEmail{destination: "buyer@example.com", verificationCode: "123456"}
	message, _, _, err = merchantStorePickupEmailMessage(email)
	require.NoError(t, err)
	bodies = merchantStoreEmailTestBodies(t, message)
	require.Len(t, bodies, 1)
	require.Contains(t, bodies["text/plain"], "123456")
	require.NotContains(t, bodies["text/plain"], "/store/claim/")
	email.verificationCode = "１２３４５６"
	_, _, _, err = merchantStorePickupEmailMessage(email)
	require.Error(t, err)
}

func TestMerchantStoreEmailSenderHasBoundedCancellation(t *testing.T) {
	merchantStoreSMTPTestSettings(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer listener.Close()
	common.SMTPPort = listener.Addr().(*net.TCPAddr).Port
	done := make(chan struct{})
	go func() {
		defer close(done)
		connection, err := listener.Accept()
		if err != nil {
			return
		}
		defer connection.Close()
		_ = connection.SetDeadline(time.Now().Add(time.Second))
		_, _ = bufio.NewReader(connection).ReadString('\n')
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	err = sendMerchantStorePickupEmail(ctx, merchantStorePickupEmail{destination: "buyer@example.com", verificationCode: "123456"})
	require.Error(t, err)
	require.Less(t, time.Since(start), time.Second)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("SMTP connection remained open after cancellation")
	}
}

func TestMerchantStoreVerificationEmailRefusesChangedAddress(t *testing.T) {
	f := merchantStoreServiceDB(t, MerchantStoreBalance)
	// Rejection happens before SMTP; this test makes no outgoing connection.
	require.Error(t, SendMerchantStoreVerificationEmail(context.Background(), f.buyer.Id, "old@example.com", "123456"))
	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", f.buyer.Id).Update("email", "new@example.com").Error)
	require.Error(t, SendMerchantStoreVerificationEmail(context.Background(), f.buyer.Id, f.buyer.Email, "123456"))
}

func TestMerchantStoreOrderSearchEmailHasIndependentPurposeAndNoOrderContent(t *testing.T) {
	merchantStoreSMTPTestSettings(t)
	message, _, destination, err := merchantStorePickupEmailMessage(merchantStorePickupEmail{destination: "search@example.test", verificationCode: "123456", orderSearch: true})
	require.NoError(t, err)
	require.Equal(t, "search@example.test", destination)
	parts := strings.SplitN(string(message), "\r\n\r\n", 2)
	content, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(parts[1], "\r\n", ""))
	require.NoError(t, err)
	require.Contains(t, string(content), "订单查询验证码：123456")
	require.Contains(t, string(content), "10 分钟")
	require.NotContains(t, string(content), "/store/claim/")
	require.NotContains(t, string(content), "订单号：")
	_, _, _, err = merchantStorePickupEmailMessage(merchantStorePickupEmail{destination: "search@example.test", verificationCode: "123456", orderSearch: true, pickupURL: "https://api.example.test/store/claim/private"})
	require.Error(t, err)
}
