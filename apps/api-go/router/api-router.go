package router

import (
	"github.com/LIghtJUNction/api.lmm.best/controller"
	"github.com/LIghtJUNction/api.lmm.best/middleware"

	// Import oauth package to register providers via init()
	_ "github.com/LIghtJUNction/api.lmm.best/oauth"

	"github.com/gin-contrib/gzip"
	"github.com/gin-gonic/gin"
)

const (
	// Wallet checkout and quote payloads contain only an amount, payment method,
	// optional discount code, or provider-specific selector. Bound them before
	// Gin decodes JSON so an authenticated client cannot grow the heap with an
	// arbitrary request body.
	topUpMutationRequestMaxBytes        = 16 << 10
	waffoPancakeMutationRequestMaxBytes = 16 << 10
	// Subscription preference and checkout requests only contain plan IDs,
	// payment-method selectors, and redirect-free metadata. Bound the entire
	// authenticated subscription surface before any ShouldBindJSON call.
	subscriptionMutationRequestMaxBytes = 16 << 10
	// Self updates only contain profile fields and console preferences. Keep the
	// decoded request bounded before UpdateSelf streams it into a map.
	userSelfMutationRequestMaxBytes = 16 << 10
	// Token mutation payloads contain only bounded metadata (name, group,
	// optional model/IP limits, or at most 100 IDs).  Keep these key-management
	// endpoints from handing an unbounded JSON stream to encoding/json.
	tokenMutationRequestMaxBytes = 16 << 10
	// OAuth state and legacy provider binding payloads contain only short
	// provider/intent/affiliate or verification-code fields. Keep these compact
	// mutations below the much larger anonymous request ceiling.
	compactOAuthRequestMaxBytes = 16 << 10
	// Open-source bounty mutations carry bounded URLs, descriptions, notes, or
	// dispute statements. The largest validated text field is 5 KiB, so keep
	// the complete JSON envelope below 16 KiB before decoding it.
	openSourceBountyMutationRequestMaxBytes = 16 << 10
	// TOTP and generic security-proof requests contain only a method, scope,
	// code, and optional flow token. Keep these authenticated mutations compact.
	securityFactorMutationRequestMaxBytes = 4 << 10
	// WebAuthn begin requests contain only a proof scope, while finish requests
	// carry a browser-generated credential response. Leave finish responses more
	// room for authenticator attestation data while remaining far below the
	// anonymous 512 KiB ceiling.
	passkeyBeginRequestMaxBytes  = 4 << 10
	passkeyFinishRequestMaxBytes = 64 << 10
	// Raw configuration imports are intentionally larger than ordinary option
	// writes, but remain bounded before decoding to keep the root-only editor
	// from becoming an unbounded heap allocation.
	rawOptionMutationRequestMaxBytes = 512 << 10
	// HeroSMS option and purchase payloads only contain a domain id, quantity,
	// or a small root-owned secret/config tuple. Keep them compact before JSON
	// decoding and upstream fan-out.
	heroSMSMutationRequestMaxBytes = 16 << 10
)

func SetApiRouter(router *gin.Engine) {
	apiRouter := router.Group("/api")
	apiRouter.Use(middleware.RouteTag("api"))
	apiRouter.Use(gzip.Gzip(gzip.DefaultCompression))
	apiRouter.Use(middleware.BodyStorageCleanup()) // 清理请求体存储
	apiRouter.Use(middleware.GlobalAPIRateLimit())
	apiRouter.Use(middleware.ConsoleAccessGate())
	// Bounty routes have their own L1 boundary and a deliberately public board.
	// Keep them outside ConsoleAccessGate so L0 callers can browse public data
	// and receive redacted empty private feeds without a page-wide 404.
	openSourceBountyApiRouter := router.Group("/api")
	openSourceBountyApiRouter.Use(middleware.RouteTag("api"))
	openSourceBountyApiRouter.Use(gzip.Gzip(gzip.DefaultCompression))
	openSourceBountyApiRouter.Use(middleware.BodyStorageCleanup())
	openSourceBountyApiRouter.Use(middleware.GlobalAPIRateLimit())
	anonymousRequestBodyLimit := middleware.AnonymousRequestBodyLimit()
	{
		apiRouter.GET("/setup", controller.GetSetup)
		apiRouter.POST("/setup", anonymousRequestBodyLimit, controller.PostSetup)
		apiRouter.GET("/livez", controller.GetLiveness)
		apiRouter.GET("/status", controller.GetStatus)
		apiRouter.GET("/uptime/status", controller.GetUptimeKumaStatus)
		apiRouter.GET("/models", middleware.UserAuth(), controller.DashboardListModels)
		apiRouter.GET("/status/test", middleware.AdminAuth(), controller.TestStatus)
		apiRouter.GET("/notice", controller.GetNotice)
		apiRouter.GET("/user-agreement", controller.GetUserAgreement)
		apiRouter.GET("/privacy-policy", controller.GetPrivacyPolicy)
		apiRouter.GET("/about", controller.GetAbout)
		//apiRouter.GET("/midjourney", controller.GetMidjourney)
		apiRouter.GET("/home_page_content", controller.GetHomePageContent)
		securityRoute := apiRouter.Group("/security")
		{
			// Public policy/statistics intentionally omit matcher patterns,
			// prompt contents, user identifiers, and per-rule event rows.
			securityRoute.GET("/policy", controller.GetPublicSecurityPolicy)
			securityRoute.GET("/stats", controller.GetPublicSecurityStats)
		}
		// Advanced-security rules are a raw JSON policy document. Keep the root
		// editor bounded before DecodeJson retains an arbitrary-sized RawMessage.
		apiRouter.PUT("/security/admin/settings", middleware.RequestBodyLimit(rawOptionMutationRequestMaxBytes), middleware.RootAuth(), middleware.DisableCache(), controller.UpdateAdvancedSecuritySettings)
		securityAdminRoute := apiRouter.Group("/security/admin")
		securityAdminRoute.Use(middleware.AdminAuth())
		{
			securityAdminRoute.GET("/policy", controller.GetAdminSecurityPolicy)
			securityAdminRoute.GET("/stats", controller.GetAdminSecurityStats)
			securityAdminRoute.GET("/events", controller.ListAdminSecurityEvents)
			securityAdminRoute.GET("/ai-reviews", controller.ListAdminAssistantSecurityReviews)
			securityAdminRoute.GET("/review-runs", controller.ListAdminAssistantReviewTasks)
			securityAdminRoute.GET("/review-runs/:task_id", controller.GetAdminAssistantReviewTask)
			securityAdminRoute.GET("/violation-fee-appeals", middleware.DisableCache(), controller.ListAdminViolationFeeAppeals)
			securityAdminRoute.POST("/violation-fee-appeals/:id/:action", middleware.CriticalRateLimit(), middleware.DisableCache(), controller.ReviewAdminViolationFeeAppeal)
		}
		releaseNoteRoute := apiRouter.Group("/release-notes")
		releaseNoteRoute.Use(middleware.UserAuth())
		{
			releaseNoteRoute.GET("/latest", middleware.DisableCache(), controller.GetLatestUnreadReleaseNote)
			releaseNoteRoute.POST("/:id/read", middleware.DisableCache(), controller.MarkReleaseNoteRead)
		}
		releaseNoteAdminRoute := apiRouter.Group("/release-notes/admin")
		releaseNoteAdminRoute.Use(middleware.AdminAuth())
		{
			releaseNoteAdminRoute.GET("", middleware.DisableCache(), controller.ListAdminReleaseNotes)
			releaseNoteAdminRoute.POST("", middleware.CriticalRateLimit(), middleware.DisableCache(), controller.PublishAdminReleaseNote)
		}
		todoRoute := apiRouter.Group("/todos")
		todoRoute.Use(middleware.UserAuth())
		{
			todoRoute.GET("", middleware.DisableCache(), controller.GetUnifiedTodos)
			todoRoute.POST("/read", middleware.DisableCache(), controller.MarkUnifiedTodosRead)
		}
		// Compatibility alias for older edge configurations. New package-managed
		// Nginx uses /internal/access-ip-policy outside the API rate-limit group.
		apiRouter.GET("/internal/access-ip-policy", middleware.DisableCache(), controller.CheckIPAccessRoutingPolicy)
		apiRouter.GET("/pricing", middleware.HeaderNavModuleAuth("pricing"), controller.GetPricing)
		perfMetricsRoute := apiRouter.Group("/perf-metrics")
		perfMetricsRoute.Use(middleware.HeaderNavModulePublicOrUserAuth("pricing"))
		{
			perfMetricsRoute.GET("/summary", controller.GetPerfMetricsSummary)
			perfMetricsRoute.GET("", controller.GetPerfMetrics)
		}
		apiRouter.GET("/rankings", middleware.HeaderNavModuleAuth("rankings"), controller.GetRankings)
		apiRouter.GET("/rankings/users", middleware.HeaderNavModuleAuth("rankings"), controller.GetUserUsageRankings)
		apiRouter.GET("/verification", middleware.EmailVerificationRateLimit(), middleware.TurnstileCheck(), controller.SendEmailVerification)
		apiRouter.GET("/reset_password", middleware.CriticalRateLimit(), middleware.TurnstileCheck(), controller.SendPasswordResetEmail)
		apiRouter.POST("/user/reset", middleware.CriticalRateLimit(), anonymousRequestBodyLimit, controller.ResetPassword)
		// OAuth routes - specific routes must come before :provider wildcard
		apiRouter.POST("/oauth/state", middleware.RequestBodyLimit(compactOAuthRequestMaxBytes), middleware.CriticalRateLimit(), middleware.DisableCache(), middleware.TryUserAuth(), controller.GenerateOAuthCode)
		// Email/code binding is a compact JSON mutation. Bound it before auth so
		// an authenticated caller cannot make encoding/json grow the heap with an
		// arbitrarily long string field.
		apiRouter.POST("/oauth/email/bind", middleware.RequestBodyLimit(userSelfMutationRequestMaxBytes), middleware.UserAuth(), middleware.CriticalRateLimit(), controller.EmailBind)
		// Non-standard OAuth (WeChat, Telegram) - keep original routes
		apiRouter.POST("/oauth/wechat/start", middleware.RequestBodyLimit(compactOAuthRequestMaxBytes), middleware.CriticalRateLimit(), middleware.DisableCache(), middleware.TryUserAuth(), controller.WeChatAuthStart)
		apiRouter.GET("/oauth/wechat", middleware.CriticalRateLimit(), middleware.DisableCache(), controller.WeChatAuth)
		apiRouter.POST("/oauth/wechat/bind", middleware.RequestBodyLimit(compactOAuthRequestMaxBytes), middleware.UserAuth(), middleware.CriticalRateLimit(), controller.WeChatBind)
		apiRouter.POST("/oauth/telegram/login/start", middleware.RequestBodyLimit(compactOAuthRequestMaxBytes), middleware.CriticalRateLimit(), middleware.DisableCache(), middleware.TryUserAuth(), controller.TelegramLoginStart)
		apiRouter.GET("/oauth/telegram/login", middleware.CriticalRateLimit(), middleware.DisableCache(), controller.TelegramLogin)
		apiRouter.POST("/oauth/telegram/bind/start", middleware.RequestBodyLimit(compactOAuthRequestMaxBytes), middleware.UserAuth(), middleware.CriticalRateLimit(), middleware.DisableCache(), controller.TelegramBindStart)
		apiRouter.GET("/oauth/telegram/bind/:flow_token", middleware.CriticalRateLimit(), middleware.DisableCache(), controller.TelegramBind)
		// Standard OAuth providers (GitHub, Discord, OIDC, LinuxDO) - unified route
		apiRouter.GET("/oauth/:provider", middleware.CriticalRateLimit(), middleware.DisableCache(), middleware.TryUserAuth(), controller.HandleOAuth)
		apiRouter.GET("/ratio_config", middleware.CriticalRateLimit(), controller.GetRatioConfig)

		apiRouter.POST("/stripe/webhook", anonymousRequestBodyLimit, controller.StripeWebhook)
		apiRouter.POST("/creem/webhook", anonymousRequestBodyLimit, controller.CreemWebhook)
		apiRouter.POST("/waffo/webhook", anonymousRequestBodyLimit, controller.WaffoWebhook)
		// :env separates test vs prod URLs so the operator can register each
		// in Pancake's matching webhook slot; handler enforces env match.
		apiRouter.POST("/waffo-pancake/webhook/:env", anonymousRequestBodyLimit, controller.WaffoPancakeWebhook)

		// Universal secure verification routes
		apiRouter.POST("/verify", middleware.UserAuth(), middleware.CriticalRateLimit(), middleware.DisableCache(), middleware.RequestBodyLimit(securityFactorMutationRequestMaxBytes), controller.UniversalVerify)
		apiRouter.POST("/verify/email", middleware.UserAuth(), middleware.CriticalRateLimit(), middleware.EmailVerificationRateLimit(), middleware.DisableCache(), controller.SendSecurityEmailVerification)

		userRoute := apiRouter.Group("/user")
		{
			userRoute.POST("/auth/refresh", middleware.SessionCookieOriginGuard(), middleware.CriticalRateLimit(), middleware.DisableCache(), controller.RefreshAuth)
			userRoute.POST("/auth/logout", middleware.SessionCookieOriginGuard(), middleware.CriticalRateLimit(), middleware.DisableCache(), controller.AuthLogout)
			userRoute.POST("/register", middleware.CriticalRateLimit(), anonymousRequestBodyLimit, middleware.TurnstileCheck(), controller.Register)
			userRoute.POST("/login", middleware.CriticalRateLimit(), middleware.DisableCache(), anonymousRequestBodyLimit, middleware.TurnstileCheck(), controller.Login)
			userRoute.POST("/login/2fa", middleware.CriticalRateLimit(), middleware.DisableCache(), anonymousRequestBodyLimit, controller.Verify2FALogin)
			userRoute.POST("/passkey/login/begin", middleware.CriticalRateLimit(), middleware.DisableCache(), anonymousRequestBodyLimit, controller.PasskeyLoginBegin)
			userRoute.POST("/passkey/login/finish", middleware.CriticalRateLimit(), middleware.DisableCache(), anonymousRequestBodyLimit, controller.PasskeyLoginFinish)
			// Appeal submission accepts either a valid browser session or the
			// disabled-account password fallback. TryUserAuth is intentionally
			// used instead of UserAuth so a disabled account can reach the
			// password-verified path after all sessions were revoked.
			userRoute.POST("/account-action-requests/appeal", middleware.CriticalRateLimit(), anonymousRequestBodyLimit, middleware.TurnstileCheck(), middleware.TryUserAuth(), controller.SubmitAccountAppeal)
			//userRoute.POST("/tokenlog", middleware.CriticalRateLimit(), controller.TokenLog)
			userRoute.POST("/epay/notify", anonymousRequestBodyLimit, controller.EpayNotify)
			userRoute.GET("/epay/notify", controller.EpayNotify)
			userRoute.GET("/groups", controller.GetUserGroups)
			// These self-service mutations accept bounded text fields. Keep the
			// body ceiling ahead of authentication so oversized requests cannot
			// consume auth work or reach JSON decoding.
			userRoute.POST("/account-action-requests", middleware.CriticalRateLimit(), middleware.DisableCache(), middleware.DecompressRequestMiddleware(), middleware.RequestBodyLimit(userSelfMutationRequestMaxBytes), middleware.UserAuth(), controller.SubmitAccountActionRequest)
			userRoute.POST("/violation-fee-appeals", middleware.CriticalRateLimit(), middleware.DisableCache(), middleware.DecompressRequestMiddleware(), middleware.RequestBodyLimit(userSelfMutationRequestMaxBytes), middleware.UserAuth(), controller.SubmitViolationFeeAppeal)

			selfRoute := userRoute.Group("/")
			selfRoute.Use(middleware.UserAuth())
			{
				selfRoute.GET("/access-ip", middleware.DisableCache(), controller.PersonalAccessIPRetired)
				selfRoute.PUT("/access-ip", middleware.CriticalRateLimit(), middleware.DisableCache(), controller.PersonalAccessIPRetired)
				selfRoute.DELETE("/access-ip", middleware.CriticalRateLimit(), middleware.DisableCache(), controller.PersonalAccessIPRetired)
				selfRoute.GET("/sessions", middleware.DisableCache(), controller.GetLoginSessions)
				selfRoute.DELETE("/sessions/:sid", middleware.DisableCache(), controller.DeleteLoginSession)
				selfRoute.POST("/sessions/revoke-others", middleware.DisableCache(), controller.RevokeOtherLoginSessions)
				selfRoute.GET("/self/groups", controller.GetUserGroups)
				selfRoute.GET("/self", controller.GetSelf)
				selfRoute.GET("/models", controller.GetUserModels)
				selfRoute.GET("/self/onboarding/todo", middleware.DisableCache(), controller.GetL1OnboardingTodo)
				selfRoute.PATCH("/self/onboarding/todo", middleware.DisableCache(), controller.PatchL1OnboardingTodo)
				selfRoute.GET("/developer-access/request", controller.GetDeveloperAccessRequest)
				selfRoute.POST("/developer-access/request", middleware.CriticalRateLimit(), middleware.DecompressRequestMiddleware(), middleware.RequestBodyLimit(userSelfMutationRequestMaxBytes), controller.SubmitDeveloperAccessRequest)
				selfRoute.GET("/account-action-requests/appeal", middleware.DisableCache(), controller.GetAccountAppeal)
				selfRoute.GET("/violation-fees", middleware.DisableCache(), controller.ListSelfViolationFeeRecords)
				selfRoute.PUT("/self", middleware.CriticalRateLimit(), middleware.DisableCache(), middleware.DecompressRequestMiddleware(), middleware.RequestBodyLimit(userSelfMutationRequestMaxBytes), controller.UpdateSelf)
				selfRoute.DELETE("/self", controller.DeleteSelf)
				selfRoute.GET("/token", middleware.CriticalRateLimit(), middleware.UserCriticalRateLimit("access-token"), middleware.DisableCache(), controller.GenerateAccessToken)
				selfRoute.GET("/passkey", controller.PasskeyStatus)
				selfRoute.POST("/passkey/register/begin", middleware.DisableCache(), middleware.RequestBodyLimit(passkeyBeginRequestMaxBytes), controller.PasskeyRegisterBegin)
				selfRoute.POST("/passkey/register/finish", middleware.DisableCache(), middleware.RequestBodyLimit(passkeyFinishRequestMaxBytes), controller.PasskeyRegisterFinish)
				selfRoute.POST("/passkey/verify/begin", middleware.DisableCache(), middleware.RequestBodyLimit(passkeyBeginRequestMaxBytes), controller.PasskeyVerifyBegin)
				selfRoute.POST("/passkey/verify/finish", middleware.DisableCache(), middleware.RequestBodyLimit(passkeyFinishRequestMaxBytes), controller.PasskeyVerifyFinish)
				selfRoute.DELETE("/passkey", middleware.DisableCache(), controller.PasskeyDelete)
				selfRoute.GET("/aff", controller.GetAffCode)
				selfRoute.GET("/topup/info", controller.GetTopUpInfo)
				selfRoute.GET("/topup/self", controller.GetUserTopUps)
				selfRoute.POST("/discount-code/validate", middleware.RequestBodyLimit(topUpMutationRequestMaxBytes), middleware.DisableCache(), controller.ValidateDiscountCode)
				selfRoute.POST("/topup", middleware.RequestBodyLimit(topUpMutationRequestMaxBytes), middleware.CriticalRateLimit(), controller.TopUp)
				selfRoute.POST("/pay", middleware.RequestBodyLimit(topUpMutationRequestMaxBytes), middleware.PaymentMethodAccessGate(), middleware.CriticalRateLimit(), controller.RequestEpay)
				selfRoute.POST("/amount", middleware.RequestBodyLimit(topUpMutationRequestMaxBytes), middleware.PaymentMethodAccessGate(), controller.RequestAmount)
				selfRoute.POST("/stripe/pay", middleware.RequestBodyLimit(topUpMutationRequestMaxBytes), middleware.PaymentMethodAccessGate(), middleware.CriticalRateLimit(), controller.RequestStripePay)
				selfRoute.POST("/stripe/amount", middleware.RequestBodyLimit(topUpMutationRequestMaxBytes), middleware.PaymentMethodAccessGate(), controller.RequestStripeAmount)
				selfRoute.POST("/creem/pay", middleware.RequestBodyLimit(topUpMutationRequestMaxBytes), middleware.PaymentMethodAccessGate(), middleware.CriticalRateLimit(), controller.RequestCreemPay)
				selfRoute.POST("/waffo/amount", middleware.RequestBodyLimit(topUpMutationRequestMaxBytes), middleware.PaymentMethodAccessGate(), controller.RequestWaffoAmount)
				selfRoute.POST("/waffo/pay", middleware.RequestBodyLimit(topUpMutationRequestMaxBytes), middleware.PaymentMethodAccessGate(), middleware.CriticalRateLimit(), controller.RequestWaffoPay)
				selfRoute.POST("/waffo-pancake/amount", middleware.RequestBodyLimit(waffoPancakeMutationRequestMaxBytes), middleware.PaymentMethodAccessGate(), controller.RequestWaffoPancakeAmount)
				selfRoute.POST("/waffo-pancake/pay", middleware.RequestBodyLimit(waffoPancakeMutationRequestMaxBytes), middleware.PaymentMethodAccessGate(), middleware.CriticalRateLimit(), controller.RequestWaffoPancakePay)
				selfRoute.POST("/aff_transfer", middleware.RequestBodyLimit(topUpMutationRequestMaxBytes), middleware.UserCriticalRateLimit("aff-transfer"), controller.TransferAffQuota)
				selfRoute.PUT("/setting", middleware.RequestBodyLimit(userSelfMutationRequestMaxBytes), controller.UpdateUserSetting)

				// 2FA routes
				selfRoute.GET("/2fa/status", controller.Get2FAStatus)
				selfRoute.POST("/2fa/setup", middleware.DisableCache(), middleware.RequestBodyLimit(securityFactorMutationRequestMaxBytes), controller.Setup2FA)
				selfRoute.POST("/2fa/enable", middleware.DisableCache(), middleware.RequestBodyLimit(securityFactorMutationRequestMaxBytes), controller.Enable2FA)
				selfRoute.POST("/2fa/disable", middleware.DisableCache(), middleware.RequestBodyLimit(securityFactorMutationRequestMaxBytes), controller.Disable2FA)
				selfRoute.POST("/2fa/backup_codes", middleware.DisableCache(), middleware.RequestBodyLimit(securityFactorMutationRequestMaxBytes), controller.RegenerateBackupCodes)

				// Check-in routes
				selfRoute.GET("/checkin", controller.GetCheckinStatus)
				selfRoute.POST("/checkin", middleware.TurnstileCheck(), controller.DoCheckin)

				// Compensation gift routes (user)
				selfRoute.GET("/gift", controller.GetAvailableGifts)
				selfRoute.POST("/gift/:id/claim", middleware.CriticalRateLimit(), controller.ClaimGift)

				// Custom OAuth bindings
				selfRoute.GET("/oauth/bindings", controller.GetUserOAuthBindings)
				selfRoute.DELETE("/oauth/bindings/:provider_id", controller.UnbindCustomOAuth)
				selfRoute.DELETE("/bindings/:binding_type", controller.ClearSelfOAuthBinding)
			}

			adminRoute := userRoute.Group("/")
			adminRoute.Use(middleware.AdminAuth())
			{
				adminRoute.GET("/", controller.GetAllUsers)
				adminRoute.GET("/topup", controller.GetAllTopUps)
				adminRoute.POST("/topup/complete", controller.AdminCompleteTopUp)
				adminRoute.GET("/search", controller.SearchUsers)
				adminRoute.GET("/:id/oauth/bindings", controller.GetUserOAuthBindingsByAdmin)
				adminRoute.DELETE("/:id/oauth/bindings/:provider_id", controller.UnbindCustomOAuthByAdmin)
				adminRoute.DELETE("/:id/bindings/:binding_type", controller.AdminClearUserBinding)
				adminRoute.GET("/:id", controller.GetUser)
				adminRoute.GET("/:id/developer-access/archives", middleware.DisableCache(), controller.ListUserDeveloperAccessRecommendationArchives)
				adminRoute.GET("/:id/assistant-profile", controller.AdminGetAssistantUserProfile)
				adminRoute.PUT("/:id/assistant-profile", middleware.RequestBodyLimit(assistantMutationRequestMaxBytes), controller.AdminUpdateAssistantUserProfile)
				adminRoute.GET("/:id/assistant-memories", controller.AdminListMemories)
				adminRoute.POST("/:id/assistant-memories", middleware.RequestBodyLimit(assistantMutationRequestMaxBytes), middleware.CriticalRateLimit(), controller.AdminCreateMemory)
				adminRoute.PUT("/:id/assistant-memories/:memoryId", middleware.RequestBodyLimit(assistantMutationRequestMaxBytes), middleware.CriticalRateLimit(), controller.AdminUpdateMemory)
				adminRoute.DELETE("/:id/assistant-memories/:memoryId", middleware.CriticalRateLimit(), controller.AdminDeleteMemory)
				adminRoute.POST("/", controller.CreateUser)
				adminRoute.POST("/manage", controller.ManageUser)
				adminRoute.PUT("/", controller.UpdateUser)
				adminRoute.DELETE("/:id", controller.DeleteUser)
				adminRoute.DELETE("/:id/reset_passkey", controller.AdminResetPasskey)

				// Admin 2FA routes
				adminRoute.GET("/2fa/stats", controller.Admin2FAStats)
				adminRoute.DELETE("/:id/2fa", controller.AdminDisable2FA)

			}
		}
		onboardingProofRoute := apiRouter.Group("/onboarding/todo")
		// Proof payloads contain only a step, client name, base URL, and group.
		// Bound the body before API-key authentication and JSON decoding so an
		// untrusted key holder cannot use this public proof endpoint to grow the
		// request allocation without limit.
		onboardingProofRoute.Use(middleware.RequestBodyLimit(userSelfMutationRequestMaxBytes), middleware.TokenAuth())
		{
			onboardingProofRoute.POST("/proof", middleware.DisableCache(), controller.PostL1OnboardingProof)
		}

		// Admin compensation gift management
		giftRoute := apiRouter.Group("/gift")
		giftRoute.Use(middleware.AdminAuth())
		{
			giftRoute.GET("/", controller.AdminGetGifts)
			giftRoute.POST("/", middleware.CriticalRateLimit(), controller.AdminCreateGift)
			giftRoute.PUT("/:id", middleware.CriticalRateLimit(), controller.AdminUpdateGift)
			giftRoute.GET("/claims", controller.AdminGetGiftClaims)
		}

		developerAccessRequestRoute := apiRouter.Group("/developer-access/requests")
		developerAccessRequestRoute.Use(middleware.AdminAuth())
		{
			developerAccessRequestRoute.GET("", controller.ListDeveloperAccessRequests)
			developerAccessRequestRoute.POST("/:id/approve", middleware.CriticalRateLimit(), controller.ApproveDeveloperAccessRequest)
			developerAccessRequestRoute.POST("/:id/reject", middleware.CriticalRateLimit(), controller.RejectDeveloperAccessRequest)
		}

		accountActionRequestRoute := apiRouter.Group("/account-action-requests")
		accountActionRequestRoute.Use(middleware.AdminAuth())
		{
			accountActionRequestRoute.GET("", middleware.DisableCache(), controller.ListAccountActionRequests)
			accountActionRequestRoute.POST("/:id/approve", middleware.CriticalRateLimit(), middleware.DisableCache(), controller.ApproveAccountActionRequest)
			accountActionRequestRoute.POST("/:id/reject", middleware.CriticalRateLimit(), middleware.DisableCache(), controller.RejectAccountActionRequest)
		}

		// Subscription billing (plans, purchase, admin management)
		subscriptionRoute := apiRouter.Group("/subscription")
		subscriptionRoute.Use(middleware.RequestBodyLimit(subscriptionMutationRequestMaxBytes), middleware.UserAuth())
		{
			subscriptionRoute.GET("/plans", controller.GetSubscriptionPlans)
			subscriptionRoute.GET("/self", controller.GetSubscriptionSelf)
			subscriptionRoute.PUT("/self/preference", controller.UpdateSubscriptionPreference)
			subscriptionRoute.POST("/balance/pay", middleware.PaymentAccessGate(), middleware.CriticalRateLimit(), controller.SubscriptionRequestBalancePay)
			subscriptionRoute.POST("/epay/pay", middleware.PaymentMethodAccessGate(), middleware.CriticalRateLimit(), controller.SubscriptionRequestEpay)
			subscriptionRoute.POST("/stripe/pay", middleware.PaymentMethodAccessGate(), middleware.CriticalRateLimit(), controller.SubscriptionRequestStripePay)
			subscriptionRoute.POST("/creem/pay", middleware.PaymentMethodAccessGate(), middleware.CriticalRateLimit(), controller.SubscriptionRequestCreemPay)
			subscriptionRoute.POST("/waffo-pancake/pay", middleware.RequestBodyLimit(waffoPancakeMutationRequestMaxBytes), middleware.PaymentMethodAccessGate(), middleware.CriticalRateLimit(), controller.SubscriptionRequestWaffoPancakePay)
		}
		subscriptionAdminRoute := apiRouter.Group("/subscription/admin")
		subscriptionAdminRoute.Use(middleware.AdminAuth())
		{
			subscriptionAdminRoute.GET("/plans", controller.AdminListSubscriptionPlans)
			subscriptionAdminRoute.POST("/plans", controller.AdminCreateSubscriptionPlan)
			subscriptionAdminRoute.PUT("/plans/:id", controller.AdminUpdateSubscriptionPlan)
			subscriptionAdminRoute.DELETE("/plans/:id", controller.AdminDeleteSubscriptionPlan)
			subscriptionAdminRoute.PATCH("/plans/:id", controller.AdminUpdateSubscriptionPlanStatus)
			subscriptionAdminRoute.POST("/bind", controller.AdminBindSubscription)
			subscriptionAdminRoute.POST("/plans/:id/subscriptions/reset", controller.AdminResetPlanSubscriptions)

			// User subscription management (admin)
			subscriptionAdminRoute.GET("/users/:id/subscriptions", controller.AdminListUserSubscriptions)
			subscriptionAdminRoute.POST("/users/:id/subscriptions", controller.AdminCreateUserSubscription)
			subscriptionAdminRoute.POST("/users/:id/subscriptions/reset", controller.AdminResetUserSubscriptionsByPlan)
			subscriptionAdminRoute.POST("/user_subscriptions/:id/invalidate", controller.AdminInvalidateUserSubscription)
			subscriptionAdminRoute.DELETE("/user_subscriptions/:id", controller.AdminDeleteUserSubscription)
		}

		// Subscription payment callbacks (no auth)
		apiRouter.POST("/subscription/epay/notify", anonymousRequestBodyLimit, controller.SubscriptionEpayNotify)
		apiRouter.GET("/subscription/epay/notify", controller.SubscriptionEpayNotify)
		apiRouter.GET("/subscription/epay/return", controller.SubscriptionEpayReturn)
		apiRouter.POST("/subscription/epay/return", anonymousRequestBodyLimit, controller.SubscriptionEpayReturn)
		optionRoute := apiRouter.Group("/option")
		optionRoute.Use(middleware.RootAuth())
		{
			optionRoute.GET("/", controller.GetOptions)
			optionRoute.PUT("/", controller.UpdateOption)
			optionRoute.POST("/validate", middleware.RequestBodyLimit(rawOptionMutationRequestMaxBytes), controller.ValidateOptions)
			optionRoute.POST("/bulk", middleware.RequestBodyLimit(rawOptionMutationRequestMaxBytes), controller.UpdateOptionsBulk)
			optionRoute.GET("/hero-sms", middleware.DisableCache(), controller.GetHeroSMSOptions)
			optionRoute.PUT("/hero-sms", middleware.DisableCache(), middleware.RequestBodyLimit(heroSMSMutationRequestMaxBytes), controller.PutHeroSMSOptions)
			// pi-lens-ignore: compiler:UndeclaredImportedName
			optionRoute.POST("/hero-sms/test", middleware.DisableCache(), middleware.RequestBodyLimit(heroSMSMutationRequestMaxBytes), controller.CheckHeroSMSOptions)
			optionRoute.DELETE("/hero-sms/key", middleware.DisableCache(), controller.DeleteHeroSMSOptionKey)
			optionRoute.GET("/project-update", controller.GetProjectUpdate)
			optionRoute.POST("/payment_compliance", controller.ConfirmPaymentCompliance)
			optionRoute.GET("/channel_affinity_cache", controller.GetChannelAffinityCacheStats)
			optionRoute.DELETE("/channel_affinity_cache", controller.ClearChannelAffinityCache)
			optionRoute.POST("/rest_model_ratio", controller.ResetModelRatio)
			// Credentials must never travel in a query string: URLs are routinely
			// copied into access logs, browser history, and tracing systems. The
			// catalog probe accepts an optional JSON body and otherwise resolves
			// the server-side WAFFO_* credentials.
			optionRoute.POST("/waffo-pancake/catalog", middleware.RequestBodyLimit(waffoPancakeMutationRequestMaxBytes), controller.ListWaffoPancakeCatalog)
			optionRoute.POST("/waffo-pancake/pair", middleware.RequestBodyLimit(waffoPancakeMutationRequestMaxBytes), controller.CreateWaffoPancakePair)
			optionRoute.POST("/waffo-pancake/save", middleware.RequestBodyLimit(waffoPancakeMutationRequestMaxBytes), controller.SaveWaffoPancake)
			optionRoute.POST("/waffo-pancake/subscription-product", middleware.RequestBodyLimit(waffoPancakeMutationRequestMaxBytes), controller.CreateWaffoPancakeSubscriptionProduct)
			optionRoute.GET("/waffo-pancake/subscription-product-options", controller.ListWaffoPancakeSubscriptionProductOptions)
		}

		heroSMSRoute := apiRouter.Group("/hero-sms")
		heroSMSRoute.Use(middleware.UserAuth())
		{
			heroSMSRoute.GET("/email/products", middleware.DisableCache(), controller.ListHeroSMSEmailProducts)
			heroSMSRoute.POST("/email/activations", middleware.DisableCache(), middleware.CriticalRateLimit(), middleware.UserCriticalRateLimit("hero-sms-email-purchase"), middleware.RequestBodyLimit(heroSMSMutationRequestMaxBytes), controller.CreateHeroSMSEmailActivations)
			heroSMSRoute.GET("/email/activations", middleware.DisableCache(), controller.ListHeroSMSEmailActivations)
			// pi-lens-ignore: compiler:UndeclaredImportedName
			heroSMSRoute.GET("/email/activations/current", middleware.DisableCache(), controller.GetCurrentHeroSMSEmailActivation)
			heroSMSRoute.GET("/email/activations/:id", middleware.DisableCache(), controller.GetHeroSMSEmailActivation)
			heroSMSRoute.POST("/email/activations/:id/refresh", middleware.DisableCache(), middleware.CriticalRateLimit(), middleware.UserCriticalRateLimit("hero-sms-email-refresh"), middleware.RequestBodyLimit(heroSMSMutationRequestMaxBytes), controller.RefreshHeroSMSEmailActivation)
			heroSMSRoute.POST("/email/activations/:id/cancel", middleware.DisableCache(), middleware.CriticalRateLimit(), middleware.UserCriticalRateLimit("hero-sms-email-cancel"), middleware.RequestBodyLimit(heroSMSMutationRequestMaxBytes), controller.CancelHeroSMSEmailActivation)
			heroSMSRoute.POST("/email/activations/:id/reorder", middleware.DisableCache(), middleware.CriticalRateLimit(), middleware.UserCriticalRateLimit("hero-sms-email-reorder"), middleware.RequestBodyLimit(heroSMSMutationRequestMaxBytes), controller.ReorderHeroSMSEmailActivation)
			heroSMSRoute.GET("/sms/countries", middleware.DisableCache(), controller.ListHeroSMSSMSCountries)
			heroSMSRoute.GET("/sms/services", middleware.DisableCache(), controller.ListHeroSMSSMSServices)
			heroSMSRoute.GET("/sms/offer", middleware.DisableCache(), controller.GetHeroSMSSMSOffer)
			heroSMSRoute.POST("/sms/orders", middleware.DisableCache(), middleware.CriticalRateLimit(), middleware.UserCriticalRateLimit("hero-sms-sms-purchase"), middleware.RequestBodyLimit(heroSMSMutationRequestMaxBytes), controller.CreateHeroSMSSMSOrder)
			heroSMSRoute.GET("/sms/orders", middleware.DisableCache(), controller.ListHeroSMSSMSOrders)
			heroSMSRoute.GET("/sms/orders/current", middleware.DisableCache(), controller.GetCurrentHeroSMSSMSOrder)
			heroSMSRoute.GET("/sms/orders/current-list", middleware.DisableCache(), controller.ListCurrentHeroSMSSMSOrders)
			heroSMSRoute.GET("/sms/orders/:id", middleware.DisableCache(), controller.GetHeroSMSSMSOrder)
			heroSMSRoute.POST("/sms/orders/:id/cancel", middleware.DisableCache(), middleware.CriticalRateLimit(), middleware.UserCriticalRateLimit("hero-sms-sms-cancel"), middleware.RequestBodyLimit(heroSMSMutationRequestMaxBytes), controller.CancelHeroSMSSMSOrder)
		}

		dynamicPricingRoute := apiRouter.Group("/dynamic_pricing")
		dynamicPricingRoute.Use(middleware.AdminAuth())
		{
			dynamicPricingRoute.GET("/status", controller.GetDynamicPricingStatus)
		}
		dynamicPricingSettingRoute := apiRouter.Group("/dynamic_pricing")
		dynamicPricingSettingRoute.Use(middleware.RootAuth())
		{
			dynamicPricingSettingRoute.PUT("/setting", controller.UpdateDynamicPricingSetting)
		}
		// Custom OAuth provider management (root only)
		customOAuthRoute := apiRouter.Group("/custom-oauth-provider")
		customOAuthRoute.Use(middleware.RootAuth())
		{
			customOAuthRoute.POST("/discovery", controller.FetchCustomOAuthDiscovery)
			customOAuthRoute.GET("/", controller.GetCustomOAuthProviders)
			customOAuthRoute.GET("/:id", controller.GetCustomOAuthProvider)
			customOAuthRoute.POST("/", controller.CreateCustomOAuthProvider)
			customOAuthRoute.PUT("/:id", controller.UpdateCustomOAuthProvider)
			customOAuthRoute.DELETE("/:id", controller.DeleteCustomOAuthProvider)
		}
		performanceRoute := apiRouter.Group("/performance")
		performanceRoute.Use(middleware.RootAuth())
		{
			performanceRoute.GET("/stats", controller.GetPerformanceStats)
			performanceRoute.DELETE("/disk_cache", controller.ClearDiskCache)
			performanceRoute.POST("/reset_stats", controller.ResetPerformanceStats)
			performanceRoute.POST("/gc", controller.ForceGC)
			performanceRoute.GET("/logs", controller.GetLogFiles)
			performanceRoute.DELETE("/logs", controller.CleanupLogFiles)
		}
		ratioSyncRoute := apiRouter.Group("/ratio_sync")
		ratioSyncRoute.Use(middleware.RootAuth())
		{
			ratioSyncRoute.GET("/channels", controller.GetSyncableChannels)
			ratioSyncRoute.POST("/fetch", controller.FetchUpstreamRatios)
		}
		registerChannelRoutes(apiRouter)
		registerAuthzRoutes(apiRouter)
		tokenRoute := apiRouter.Group("/token")
		tokenRoute.Use(middleware.UserAuth())
		{
			tokenRoute.GET("/", controller.GetAllTokens)
			tokenRoute.GET("/search", middleware.SearchRateLimit(), controller.SearchTokens)
			tokenRoute.GET("/auto-groups", controller.GetTokenAutoGroups)
			tokenRoute.GET("/:id", controller.GetToken)
			tokenRoute.POST("/:id/key", middleware.CriticalRateLimit(), middleware.DisableCache(), controller.GetTokenKey)
			tokenRoute.POST("/", middleware.RequestBodyLimit(tokenMutationRequestMaxBytes), controller.AddToken)
			tokenRoute.PUT("/", middleware.RequestBodyLimit(tokenMutationRequestMaxBytes), controller.UpdateToken)
			tokenRoute.DELETE("/:id", controller.DeleteToken)
			tokenRoute.POST("/batch", middleware.RequestBodyLimit(tokenMutationRequestMaxBytes), controller.DeleteTokenBatch)
			tokenRoute.POST("/batch/keys", middleware.RequestBodyLimit(tokenMutationRequestMaxBytes), middleware.CriticalRateLimit(), middleware.DisableCache(), controller.GetTokenKeysBatch)
		}

		usageRoute := apiRouter.Group("/usage")
		usageRoute.Use(middleware.CORS(), middleware.CriticalRateLimit())
		{
			tokenUsageRoute := usageRoute.Group("/token")
			tokenUsageRoute.Use(middleware.TokenAuthReadOnly())
			{
				tokenUsageRoute.GET("/", controller.GetTokenUsage)
			}
		}

		redemptionRoute := apiRouter.Group("/redemption")
		redemptionRoute.Use(middleware.AdminAuth())
		{
			redemptionRoute.GET("/", controller.GetAllRedemptions)
			redemptionRoute.GET("/search", controller.SearchRedemptions)
			redemptionRoute.GET("/:id", controller.GetRedemption)
			redemptionRoute.POST("/", controller.AddRedemption)
			redemptionRoute.PUT("/", controller.UpdateRedemption)
			redemptionRoute.DELETE("/invalid", controller.DeleteInvalidRedemption)
			redemptionRoute.DELETE("/:id", controller.DeleteRedemption)
		}

		discountCodeRoute := apiRouter.Group("/discount-code")
		discountCodeRoute.Use(middleware.AdminAuth())
		{
			discountCodeRoute.GET("/", controller.GetAllDiscountCodes)
			discountCodeRoute.GET("/search", controller.SearchDiscountCodes)
			discountCodeRoute.GET("/:id", controller.GetDiscountCode)
			discountCodeRoute.POST("/", controller.AddDiscountCode)
			discountCodeRoute.POST("/batch", controller.AddDiscountCodes)
			discountCodeRoute.PUT("/", controller.UpdateDiscountCode)
			// pi-lens-ignore: compiler:UndeclaredImportedName
			discountCodeRoute.DELETE("/exhausted", middleware.DisableCache(), middleware.CriticalRateLimit(), controller.DeleteExhaustedDiscountCodes)
			discountCodeRoute.DELETE("/:id", controller.DeleteDiscountCode)
		}

		openSourceBountyPublicRoute := openSourceBountyApiRouter.Group("/open-source-bounties")
		openSourceBountyPublicRoute.Use(middleware.TryUserAuth())
		{
			openSourceBountyPublicRoute.GET("", controller.ListOpenSourceBounties)
			openSourceBountyPublicRoute.GET("/projects/:id", controller.GetOpenSourceBounty)
			openSourceBountyPublicRoute.GET("/config", controller.GetOpenSourceBountyConfig)
		}

		openSourceBountyRoute := openSourceBountyApiRouter.Group("/open-source-bounties")
		openSourceBountyRoute.Use(middleware.UserAuth(), requireOpenSourceBountyDeveloperAccess())
		{
			openSourceBountyRoute.GET("/mcp-token", middleware.DisableCache(), controller.GetOpenSourceBountyMCPToken)
			openSourceBountyRoute.POST("/mcp-token", middleware.CriticalRateLimit(), middleware.DisableCache(), controller.RotateOpenSourceBountyMCPToken)
			openSourceBountyRoute.DELETE("/mcp-token", middleware.CriticalRateLimit(), middleware.DisableCache(), controller.RevokeOpenSourceBountyMCPToken)
			openSourceBountyRoute.POST("", middleware.RequestBodyLimit(openSourceBountyMutationRequestMaxBytes), controller.CreateOpenSourceBounty)
			openSourceBountyRoute.GET("/mine", controller.ListOwnedOpenSourceBounties)
			openSourceBountyRoute.GET("/accepted", controller.ListAcceptedOpenSourceBounties)
			openSourceBountyRoute.GET("/notifications", middleware.DisableCache(), controller.ListOpenSourceBountyNotifications)
			openSourceBountyRoute.POST("/notifications/read", controller.MarkOpenSourceBountyNotificationsRead)
			openSourceBountyRoute.GET("/tips/received", middleware.DisableCache(), controller.ListOpenSourceBountyTipNotifications)
			openSourceBountyRoute.POST("/tips/received/read", controller.MarkOpenSourceBountyTipNotificationsRead)
			openSourceBountyRoute.POST("/tips/:tip_id/thank", controller.ThankOpenSourceBountyTip)
			openSourceBountyRoute.GET("/disputes/mine", controller.ListMyOpenSourceBountyDisputes)
			openSourceBountyRoute.GET("/disputes/admin", middleware.AdminAuth(), controller.ListAdminOpenSourceBountyDisputes)
			openSourceBountyRoute.POST("/disputes/:dispute_id/resolve", middleware.AdminAuth(), middleware.CriticalRateLimit(), middleware.RequestBodyLimit(openSourceBountyMutationRequestMaxBytes), controller.ResolveOpenSourceBountyDispute)
			openSourceBountyRoute.PUT("/projects/:id", middleware.RequestBodyLimit(openSourceBountyMutationRequestMaxBytes), controller.UpdateOpenSourceBounty)
			openSourceBountyRoute.DELETE("/projects/:id", controller.DeleteOpenSourceBounty)
			openSourceBountyRoute.POST("/projects/:id/publish", middleware.CriticalRateLimit(), controller.PublishOpenSourceBounty)
			openSourceBountyRoute.POST("/projects/:id/pause", controller.PauseOpenSourceBounty)
			openSourceBountyRoute.POST("/projects/:id/resume", controller.ResumeOpenSourceBounty)
			openSourceBountyRoute.POST("/projects/:id/close", middleware.CriticalRateLimit(), controller.CloseOpenSourceBounty)
			openSourceBountyRoute.POST("/projects/:id/archive", controller.ArchiveOpenSourceBounty)
			openSourceBountyRoute.POST("/projects/:id/unarchive", controller.UnarchiveOpenSourceBounty)
			openSourceBountyRoute.POST("/projects/:id/accept", middleware.RequestBodyLimit(openSourceBountyMutationRequestMaxBytes), controller.AcceptOpenSourceBounty)
			openSourceBountyRoute.POST("/projects/:id/submit", middleware.RequestBodyLimit(openSourceBountyMutationRequestMaxBytes), controller.SubmitOpenSourceBountyChallenge)
			openSourceBountyRoute.POST("/challenges/:challenge_id/withdraw", controller.WithdrawOpenSourceBountyChallenge)
			openSourceBountyRoute.POST("/challenges/:challenge_id/cancel", controller.CancelOpenSourceBountyChallenge)
			openSourceBountyRoute.POST("/challenges/:challenge_id/approve", middleware.CriticalRateLimit(), middleware.RequestBodyLimit(openSourceBountyMutationRequestMaxBytes), controller.ApproveOpenSourceBountyChallenge)
			openSourceBountyRoute.POST("/challenges/:challenge_id/reject", middleware.RequestBodyLimit(openSourceBountyMutationRequestMaxBytes), controller.RejectOpenSourceBountyChallenge)
			openSourceBountyRoute.POST("/challenges/:challenge_id/tip", middleware.CriticalRateLimit(), middleware.RequestBodyLimit(openSourceBountyMutationRequestMaxBytes), controller.TipOpenSourceBountyChallenge)
			openSourceBountyRoute.POST("/challenges/:challenge_id/rate-owner", middleware.RequestBodyLimit(openSourceBountyMutationRequestMaxBytes), controller.RateOpenSourceBountyOwner)
			openSourceBountyRoute.POST("/challenges/:challenge_id/disputes", middleware.CriticalRateLimit(), middleware.RequestBodyLimit(openSourceBountyMutationRequestMaxBytes), controller.OpenOpenSourceBountyDispute)
		}

		publicRelayPublicRoute := openSourceBountyApiRouter.Group("/public-relays")
		publicRelayPublicRoute.Use(middleware.TryUserAuth())
		{
			publicRelayPublicRoute.GET("", controller.ListPublicRelayContributions)
			publicRelayPublicRoute.GET("/config", controller.GetPublicRelayConfig)
			publicRelayPublicRoute.GET("/:id/reviews", controller.ListPublicRelayReviews)
		}
		publicRelayUserRoute := openSourceBountyApiRouter.Group("/public-relays")
		publicRelayUserRoute.Use(middleware.UserAuth())
		{
			publicRelayUserRoute.POST("", middleware.CriticalRateLimit(), middleware.RequestBodyLimit(256<<10), controller.CreatePublicRelayContribution)
			publicRelayUserRoute.GET("/mine", controller.ListMyPublicRelayContributions)
			publicRelayUserRoute.GET("/routing", controller.GetPublicRelayRouting)
			publicRelayUserRoute.PUT("/routing", middleware.CriticalRateLimit(), middleware.RequestBodyLimit(8<<10), controller.UpdatePublicRelayRouting)
			publicRelayUserRoute.POST("/:id/report", middleware.CriticalRateLimit(), middleware.RequestBodyLimit(8<<10), controller.ReportPublicRelayContribution)
			publicRelayUserRoute.POST("/:id/review", middleware.CriticalRateLimit(), middleware.RequestBodyLimit(8<<10), controller.RatePublicRelay)
			publicRelayUserRoute.POST("/:id/tip", middleware.CriticalRateLimit(), middleware.RequestBodyLimit(4<<10), controller.TipPublicRelay)
			publicRelayUserRoute.POST("/:id/withdraw", middleware.CriticalRateLimit(), middleware.RequestBodyLimit(4<<10), controller.WithdrawPublicRelayContributionReward)
		}
		publicRelayAdminRoute := openSourceBountyApiRouter.Group("/public-relays/admin")
		publicRelayAdminRoute.Use(middleware.AdminAuth())
		{
			publicRelayAdminRoute.GET("", controller.ListAdminPublicRelayContributions)
			publicRelayAdminRoute.POST("/:id/review", middleware.CriticalRateLimit(), middleware.RequestBodyLimit(8<<10), controller.ReviewAdminPublicRelayContribution)
			publicRelayAdminRoute.POST("/:id/link-channel/:channel_id", middleware.CriticalRateLimit(), controller.LinkAdminPublicRelayChannel)
			publicRelayAdminRoute.GET("/reports", controller.ListAdminPublicRelayReports)
			publicRelayAdminRoute.POST("/reports/:id/review", middleware.CriticalRateLimit(), middleware.RequestBodyLimit(8<<10), controller.ReviewAdminPublicRelayReport)
		}
		logRoute := apiRouter.Group("/log")
		logRoute.GET("/", middleware.AdminAuth(), controller.GetAllLogs)
		logRoute.GET("/stat", middleware.AdminAuth(), controller.GetLogsStat)
		logRoute.GET("/self/stat", middleware.UserAuth(), controller.GetLogsSelfStat)
		logRoute.GET("/channel_affinity_usage_cache", middleware.AdminAuth(), controller.GetChannelAffinityUsageCacheStats)
		// pi-lens-ignore: deprecated:default
		logRoute.GET("/search", middleware.AdminAuth(), controller.SearchAllLogs)

		financeRoute := apiRouter.Group("/finance")
		financeRoute.Use(middleware.AdminAuth(), middleware.DisableCache())
		{
			financeRoute.GET("/export", controller.ExportFinancialData)
			financeRoute.GET("/overview", controller.GetFinanceOverview)
			financeRoute.GET("/users", controller.GetFinanceUsers)
			financeRoute.GET("/users/:user_id", controller.GetFinanceUser)
			financeRoute.GET("/entries", controller.ListFinanceEntries)
			financeRoute.POST("/entries", controller.CreateFinanceEntry)
			financeRoute.POST("/entries/:entry_id/reverse", controller.ReverseFinanceEntry)
			financeRoute.GET("/payment-methods", controller.ListFinancePaymentMethods)
			financeRoute.PUT("/payment-methods/:method", controller.UpdateFinancePaymentMethod)
		}
		logRoute.GET("/self", middleware.UserAuth(), controller.GetUserLogs)
		// pi-lens-ignore: deprecated:default
		logRoute.GET("/self/search", middleware.UserAuth(), middleware.SearchRateLimit(), controller.SearchUserLogs)

		systemTaskRoute := apiRouter.Group("/system-task")
		systemTaskRoute.Use(middleware.RootAuth())
		{
			systemTaskRoute.POST("/log-cleanup", controller.CreateLogCleanupSystemTask)
			systemTaskRoute.GET("/list", controller.ListSystemTasks)
			systemTaskRoute.GET("/current", controller.GetCurrentSystemTask)
			systemTaskRoute.GET("/:task_id", controller.GetSystemTask)
		}
		systemInfoRoute := apiRouter.Group("/system-info")
		systemInfoRoute.Use(middleware.RootAuth())
		{
			systemInfoRoute.GET("/instances", controller.ListSystemInstances)
			systemInfoRoute.DELETE("/stale-instances", controller.DeleteStaleSystemInstances)
			systemInfoRoute.DELETE("/instances/:node_name", controller.DeleteStaleSystemInstance)
		}

		dataRoute := apiRouter.Group("/data")
		dataRoute.GET("/", middleware.AdminAuth(), controller.GetAllQuotaDates)
		dataRoute.GET("/users", middleware.AdminAuth(), controller.GetQuotaDatesByUser)
		dataRoute.GET("/self", middleware.UserAuth(), controller.GetUserQuotaDates)
		dataRoute.GET("/flow", middleware.AdminAuth(), controller.GetAllFlowQuotaDates)
		dataRoute.GET("/flow/self", middleware.UserAuth(), controller.GetUserFlowQuotaDates)

		logRoute.Use(middleware.CORS(), middleware.CriticalRateLimit())
		{
			logRoute.GET("/token", middleware.TokenAuthReadOnly(), controller.GetLogByKey)
		}
		groupRoute := apiRouter.Group("/group")
		groupRoute.Use(middleware.AdminAuth())
		{
			groupRoute.GET("/", controller.GetGroups)
		}

		prefillGroupRoute := apiRouter.Group("/prefill_group")
		prefillGroupRoute.Use(middleware.AdminAuth())
		{
			prefillGroupRoute.GET("/", controller.GetPrefillGroups)
			prefillGroupRoute.POST("/", controller.CreatePrefillGroup)
			prefillGroupRoute.PUT("/", controller.UpdatePrefillGroup)
			prefillGroupRoute.DELETE("/:id", controller.DeletePrefillGroup)
		}

		mjRoute := apiRouter.Group("/mj")
		mjRoute.GET("/self", middleware.UserAuth(), controller.GetUserMidjourney)
		mjRoute.GET("/", middleware.AdminAuth(), controller.GetAllMidjourney)

		taskRoute := apiRouter.Group("/task")
		{
			taskRoute.GET("/self", middleware.UserAuth(), controller.GetUserTask)
			taskRoute.GET("/", middleware.AdminAuth(), controller.GetAllTask)
		}

		vendorRoute := apiRouter.Group("/vendors")
		vendorRoute.Use(middleware.AdminAuth())
		{
			vendorRoute.GET("/", controller.GetAllVendors)
			vendorRoute.GET("/search", controller.SearchVendors)
			vendorRoute.GET("/:id", controller.GetVendorMeta)
			vendorRoute.POST("/", controller.CreateVendorMeta)
			vendorRoute.PUT("/", controller.UpdateVendorMeta)
			vendorRoute.DELETE("/:id", controller.DeleteVendorMeta)
		}

		modelsRoute := apiRouter.Group("/models")
		modelsRoute.Use(middleware.AdminAuth())
		{
			modelsRoute.GET("/sync_upstream/preview", controller.SyncUpstreamPreview)
			modelsRoute.POST("/sync_upstream", controller.SyncUpstreamModels)
			modelsRoute.GET("/missing", controller.GetMissingModels)
			modelsRoute.GET("/", controller.GetAllModelsMeta)
			modelsRoute.GET("/search", controller.SearchModelsMeta)
			modelsRoute.GET("/:id", controller.GetModelMeta)
			modelsRoute.POST("/", controller.CreateModelMeta)
			modelsRoute.PUT("/", controller.UpdateModelMeta)
			modelsRoute.DELETE("/:id", controller.DeleteModelMeta)
		}

		// Deployments (model deployment management)
		deploymentsRoute := apiRouter.Group("/deployments")
		deploymentsRoute.Use(middleware.AdminAuth())
		{
			deploymentsRoute.GET("/settings", controller.GetModelDeploymentSettings)
			deploymentsRoute.POST("/settings/test-connection", controller.TestIoNetConnection)
			deploymentsRoute.GET("/", controller.GetAllDeployments)
			deploymentsRoute.GET("/search", controller.SearchDeployments)
			deploymentsRoute.POST("/test-connection", controller.TestIoNetConnection)
			deploymentsRoute.GET("/hardware-types", controller.GetHardwareTypes)
			deploymentsRoute.GET("/locations", controller.GetLocations)
			deploymentsRoute.GET("/available-replicas", controller.GetAvailableReplicas)
			deploymentsRoute.POST("/price-estimation", controller.GetPriceEstimation)
			deploymentsRoute.GET("/check-name", controller.CheckClusterNameAvailability)
			deploymentsRoute.POST("/", controller.CreateDeployment)

			deploymentsRoute.GET("/:id", controller.GetDeployment)
			deploymentsRoute.GET("/:id/logs", controller.GetDeploymentLogs)
			deploymentsRoute.GET("/:id/containers", controller.ListDeploymentContainers)
			deploymentsRoute.GET("/:id/containers/:container_id", controller.GetContainerDetails)
			deploymentsRoute.PUT("/:id", controller.UpdateDeployment)
			deploymentsRoute.PUT("/:id/name", controller.UpdateDeploymentName)
			deploymentsRoute.POST("/:id/extend", controller.ExtendDeployment)
			deploymentsRoute.DELETE("/:id", controller.DeleteDeployment)
		}
	}
}
