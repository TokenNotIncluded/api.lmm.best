package operation_setting

import "github.com/LIghtJUNction/api.lmm.best/setting/config"

type PaymentSetting struct {
	AmountOptions  PaymentAmountOptions  `json:"amount_options"`
	AmountDiscount PaymentAmountDiscount `json:"amount_discount"` // Exact preset amounts in the retained configuration unit.

	ComplianceConfirmed    bool   `json:"compliance_confirmed"`
	ComplianceTermsVersion string `json:"compliance_terms_version"`
	ComplianceConfirmedAt  int64  `json:"compliance_confirmed_at"`
	ComplianceConfirmedBy  int    `json:"compliance_confirmed_by"`
	ComplianceConfirmedIP  string `json:"compliance_confirmed_ip"`
}

const CurrentComplianceTermsVersion = "v1"

// 默认配置
var paymentSetting = PaymentSetting{
	AmountOptions:  PaymentAmountOptions{"10", "20", "50", "100", "200", "500"},
	AmountDiscount: PaymentAmountDiscount{},
}

func init() {
	// 注册到全局配置管理器
	config.GlobalConfig.Register("payment_setting", &paymentSetting)
}

func GetPaymentSetting() *PaymentSetting {
	return &paymentSetting
}

func IsPaymentComplianceConfirmed() bool {
	return paymentSetting.ComplianceConfirmed &&
		paymentSetting.ComplianceTermsVersion == CurrentComplianceTermsVersion
}
