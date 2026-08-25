package router

import (
	"github.com/LIghtJUNction/api.lmm.best/constant"
	"github.com/LIghtJUNction/api.lmm.best/controller"
	"github.com/LIghtJUNction/api.lmm.best/middleware"
	"github.com/LIghtJUNction/api.lmm.best/relay"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/types"

	"github.com/gin-gonic/gin"
)

const (
	assistantRequestMaxBytes         = 64 << 10
	assistantMutationRequestMaxBytes = 16 << 10
)

func SetRelayRouter(router *gin.Engine) {
	router.Use(middleware.CORS())
	router.Use(middleware.DecompressRequestMiddleware())
	router.Use(middleware.BodyStorageCleanup()) // 清理请求体存储
	router.Use(middleware.StatsMiddleware())
	// https://platform.openai.com/docs/api-reference/introduction
	modelsRouter := router.Group("/v1/models")
	modelsRouter.Use(middleware.RouteTag("relay"))
	modelsRouter.Use(middleware.TokenAuth())
	{
		modelsRouter.GET("", func(c *gin.Context) {
			switch {
			case c.GetHeader("x-api-key") != "" && c.GetHeader("anthropic-version") != "":
				controller.ListModels(c, constant.ChannelTypeAnthropic)
			case c.GetHeader("x-goog-api-key") != "" || c.Query("key") != "": // 单独的适配
				// Gemini's OpenAI-compatible model catalog is a collection endpoint;
				// RetrieveModel expects a :model path parameter and returned an empty
				// error-shaped response for `/v1/models`.
				controller.ListModels(c, constant.ChannelTypeGemini)
			default:
				controller.ListModels(c, constant.ChannelTypeOpenAI)
			}
		})

		modelsRouter.GET("/:model", func(c *gin.Context) {
			switch {
			case c.GetHeader("x-api-key") != "" && c.GetHeader("anthropic-version") != "":
				controller.RetrieveModel(c, constant.ChannelTypeAnthropic)
			default:
				controller.RetrieveModel(c, constant.ChannelTypeOpenAI)
			}
		})
	}

	geminiRouter := router.Group("/v1beta/models")
	geminiRouter.Use(middleware.RouteTag("relay"))
	geminiRouter.Use(middleware.TokenAuth())
	{
		geminiRouter.GET("", func(c *gin.Context) {
			controller.ListModels(c, constant.ChannelTypeGemini)
		})
	}

	geminiCompatibleRouter := router.Group("/v1beta/openai/models")
	geminiCompatibleRouter.Use(middleware.RouteTag("relay"))
	geminiCompatibleRouter.Use(middleware.TokenAuth())
	{
		geminiCompatibleRouter.GET("", func(c *gin.Context) {
			controller.ListModels(c, constant.ChannelTypeOpenAI)
		})
	}

	playgroundRouter := router.Group("/pg")
	playgroundRouter.Use(middleware.RouteTag("relay"))
	playgroundRouter.Use(middleware.SystemPerformanceCheck())
	playgroundRouter.Use(middleware.UserAuth(), middleware.Distribute())
	{
		playgroundRouter.POST("/chat/completions", controller.Playground)
	}
	// Keep route-specific body ceilings before Distribute reads the model/group
	// envelope. Editing accepts up to eight 10 MB reference images plus
	// multipart overhead; both paths retain normal relay billing.
	playgroundImageRouter := router.Group("/pg/images")
	playgroundImageRouter.Use(middleware.RouteTag("relay"))
	playgroundImageRouter.Use(middleware.SystemPerformanceCheck())
	playgroundImageRouter.Use(middleware.UserAuth())
	playgroundImageRouter.POST("/generations", middleware.RequestBodyLimit(32<<10), middleware.Distribute(), controller.PlaygroundImage)
	playgroundImageRouter.POST("/edits", middleware.RequestBodyLimit(82<<20), middleware.Distribute(), controller.PlaygroundImageEdit)
	assistantPresetRouter := router.Group("/api/assistant/pre-conversation-presets")
	assistantPresetRouter.Use(middleware.RouteTag("api"))
	assistantPresetRouter.Use(middleware.SystemPerformanceCheck())
	assistantPresetRouter.Use(middleware.TryUserAuth())
	{
		assistantPresetRouter.GET("", controller.GetPromptPresets)
		assistantPresetRouter.POST("/:id/click", middleware.CriticalRateLimit(), controller.CountPromptPresetClick)
	}
	assistantRouter := router.Group("/api/assistant")
	assistantRouter.Use(middleware.RouteTag("relay"))
	assistantRouter.Use(middleware.SystemPerformanceCheck())
	assistantRouter.Use(middleware.UserAuth())
	{
		assistantRouter.GET("/status", controller.GetAssistantStatus)
		assistantRouter.GET("/models", middleware.AdminAuth(), controller.GetAssistantModels)
		assistantRouter.GET("/offers", controller.GetAssistantPlanOffers)
		assistantRouter.GET("/journey", middleware.DisableCache(), controller.GetAssistantJourney)
		assistantRouter.GET("/new-user-gift", middleware.DisableCache(), controller.GetAssistantNewUserGift)
		assistantRouter.POST("/new-user-gift/claim", middleware.UserCriticalRateLimit("assistant-new-user-gift"), middleware.DisableCache(), controller.ClaimAssistantNewUserGift)
		assistantRouter.GET("/weekly-discount", middleware.DisableCache(), controller.GetAssistantWeeklyDiscount)
		assistantRouter.POST("/weekly-discount/claim", middleware.UserCriticalRateLimit("assistant-weekly-discount"), middleware.DisableCache(), controller.ClaimAssistantWeeklyDiscount)
		assistantRouter.POST("/chat", middleware.UserCriticalRateLimit("assistant"), middleware.RequestBodyLimit(assistantRequestMaxBytes), controller.PrepareAssistantRequest, middleware.Distribute(), controller.AssistantChat)
		assistantRouter.GET("/conversations", middleware.DisableCache(), controller.ListAssistantConversations)
		assistantRouter.GET("/conversations/:id", middleware.DisableCache(), controller.GetAssistantConversationHistory)
		assistantRouter.POST("/conversations/:id/archive", middleware.DisableCache(), controller.ArchiveAssistantConversation)
		assistantRouter.POST("/conversations/:id/unarchive", middleware.DisableCache(), controller.UnarchiveAssistantConversation)
		assistantRouter.GET("/cards/:id/reveal", middleware.CriticalRateLimit(), middleware.DisableCache(), controller.RevealAssistantSecureCard)
		assistantRouter.GET("/handoffs/self", middleware.DisableCache(), controller.GetAssistantHandoff)
		assistantRouter.POST("/handoffs", middleware.RequestBodyLimit(assistantMutationRequestMaxBytes), middleware.UserCriticalRateLimit("assistant-handoff"), middleware.DisableCache(), controller.SubmitAssistantHandoff)
		assistantRouter.POST("/tools/prepare-key", middleware.RequestBodyLimit(assistantMutationRequestMaxBytes), middleware.ConsoleAccessGate(), middleware.UserCriticalRateLimit("assistant-prepare-key"), middleware.DisableCache(), controller.PrepareAssistantDefaultKey)
		assistantRouter.POST("/tools/create-key", middleware.RequestBodyLimit(assistantMutationRequestMaxBytes), middleware.ConsoleAccessGate(), middleware.UserCriticalRateLimit("assistant-create-key"), middleware.DisableCache(), controller.CreateAssistantDefaultKey)
		assistantRouter.POST("/drawing/generate", middleware.UserCriticalRateLimit("assistant-drawing"), middleware.RequestBodyLimit(8<<10), middleware.DisableCache(), controller.GenerateAssistantDrawing)
	}
	// Model prices include group ratios and therefore can disclose the same
	// discounted console inventory as /api/pricing.  Keep this read behind the
	// developer-access boundary before UserAuth so anonymous and L0 callers
	// receive the generic 404 instead of a pricing response.
	assistantPricingRouter := router.Group("/api/assistant")
	assistantPricingRouter.Use(middleware.RouteTag("relay"))
	assistantPricingRouter.Use(middleware.SystemPerformanceCheck())
	assistantPricingRouter.Use(middleware.ConsoleAccessGate())
	assistantPricingRouter.Use(middleware.UserAuth())
	assistantPricingRouter.GET("/pricing", controller.GetAssistantPricing)
	assistantAdminRouter := router.Group("/api/assistant/admin")
	assistantAdminRouter.Use(middleware.RouteTag("api"))
	assistantAdminRouter.Use(middleware.AdminAuth())
	{
		assistantAdminRouter.POST("/apply", middleware.RequestBodyLimit(assistantMutationRequestMaxBytes), middleware.CriticalRateLimit(), middleware.DisableCache(), controller.ApplyAssistantAdminChange)
		assistantAdminRouter.GET("/handoffs", controller.AdminListAssistantHandoffs)
		assistantAdminRouter.POST("/handoffs/:id/resolve", middleware.RequestBodyLimit(assistantMutationRequestMaxBytes), middleware.CriticalRateLimit(), controller.AdminResolveAssistantHandoff)
		assistantAdminRouter.GET("/intents", controller.AdminGetAssistantIntentSummary)
		assistantAdminRouter.GET("/first-questions", middleware.DisableCache(), controller.AdminGetAssistantFirstQuestionSummary)
		assistantAdminRouter.GET("/profiles", controller.AdminGetAssistantProfileSummary)
		assistantAdminRouter.GET("/funding", controller.AdminGetAssistantFundingSummary)
		assistantAdminRouter.GET("/review", middleware.DisableCache(), controller.AdminGetAssistantReview)
		assistantAdminRouter.POST("/review/run", middleware.CriticalRateLimit(), middleware.DisableCache(), controller.AdminRunAssistantReview)
		assistantAdminRouter.GET("/request-reviews", middleware.DisableCache(), controller.AdminListAssistantRequestReviews)
		assistantAdminRouter.POST("/users/:user_id/request-reviews/reset", middleware.CriticalRateLimit(), middleware.DisableCache(), controller.AdminResetAssistantRequestReviewViolations)
	}
	relayV1Router := router.Group("/v1")
	relayV1Router.Use(middleware.RouteTag("relay"))
	relayV1Router.Use(middleware.SystemPerformanceCheck())
	relayV1Router.Use(middleware.TokenAuth())
	relayV1Router.Use(middleware.ModelRequestRateLimit())
	{
		// Channel selection is delayed until response.create supplies the model.
		relayV1Router.GET("/responses", controller.ResponsesWebSocket)
	}
	{
		// WebSocket 路由（统一到 Relay）
		wsRouter := relayV1Router.Group("")
		wsRouter.Use(middleware.Distribute())
		wsRouter.GET("/realtime", func(c *gin.Context) {
			controller.Relay(c, types.RelayFormatOpenAIRealtime)
		})
	}
	{
		//http router
		httpRouter := relayV1Router.Group("")
		httpRouter.Use(middleware.Distribute())

		// claude related routes
		httpRouter.POST("/messages", func(c *gin.Context) {
			controller.Relay(c, types.RelayFormatClaude)
		})

		// chat related routes
		httpRouter.POST("/completions", func(c *gin.Context) {
			controller.Relay(c, types.RelayFormatOpenAI)
		})
		httpRouter.POST("/chat/completions", func(c *gin.Context) {
			controller.Relay(c, types.RelayFormatOpenAI)
		})

		// response related routes
		httpRouter.POST("/responses", func(c *gin.Context) {
			controller.Relay(c, types.RelayFormatOpenAIResponses)
		})
		httpRouter.POST("/responses/compact", func(c *gin.Context) {
			controller.Relay(c, types.RelayFormatOpenAIResponsesCompaction)
		})

		// alpha search related routes (Codex standalone web search)
		httpRouter.POST("/alpha/search", func(c *gin.Context) {
			controller.Relay(c, types.RelayFormatOpenAIAlphaSearch)
		})

		// image related routes
		httpRouter.POST("/edits", func(c *gin.Context) {
			controller.Relay(c, types.RelayFormatOpenAIImage)
		})
		httpRouter.POST("/images/generations", func(c *gin.Context) {
			controller.Relay(c, types.RelayFormatOpenAIImage)
		})
		httpRouter.POST("/images/edits", func(c *gin.Context) {
			controller.Relay(c, types.RelayFormatOpenAIImage)
		})

		// embedding related routes
		httpRouter.POST("/embeddings", func(c *gin.Context) {
			controller.Relay(c, types.RelayFormatEmbedding)
		})

		// audio related routes
		httpRouter.POST("/audio/transcriptions", func(c *gin.Context) {
			controller.Relay(c, types.RelayFormatOpenAIAudio)
		})
		httpRouter.POST("/audio/translations", func(c *gin.Context) {
			controller.Relay(c, types.RelayFormatOpenAIAudio)
		})
		httpRouter.POST("/audio/speech", func(c *gin.Context) {
			controller.Relay(c, types.RelayFormatOpenAIAudio)
		})

		// rerank related routes
		httpRouter.POST("/rerank", func(c *gin.Context) {
			controller.Relay(c, types.RelayFormatRerank)
		})

		// gemini relay routes
		httpRouter.POST("/engines/:model/embeddings", func(c *gin.Context) {
			controller.Relay(c, types.RelayFormatGemini)
		})
		httpRouter.POST("/models/*path", func(c *gin.Context) {
			controller.Relay(c, types.RelayFormatGemini)
		})

		// other relay routes
		httpRouter.POST("/moderations", func(c *gin.Context) {
			controller.Relay(c, types.RelayFormatOpenAI)
		})

		// not implemented
		httpRouter.POST("/images/variations", controller.RelayNotImplemented)
		httpRouter.GET("/files", controller.RelayNotImplemented)
		httpRouter.POST("/files", controller.RelayNotImplemented)
		httpRouter.DELETE("/files/:id", controller.RelayNotImplemented)
		httpRouter.GET("/files/:id", controller.RelayNotImplemented)
		httpRouter.GET("/files/:id/content", controller.RelayNotImplemented)
		httpRouter.POST("/fine-tunes", controller.RelayNotImplemented)
		httpRouter.GET("/fine-tunes", controller.RelayNotImplemented)
		httpRouter.GET("/fine-tunes/:id", controller.RelayNotImplemented)
		httpRouter.POST("/fine-tunes/:id/cancel", controller.RelayNotImplemented)
		httpRouter.GET("/fine-tunes/:id/events", controller.RelayNotImplemented)
		httpRouter.DELETE("/models/:model", controller.RelayNotImplemented)
	}

	relayMjRouter := router.Group("/mj")
	relayMjRouter.Use(middleware.RouteTag("relay"))
	relayMjRouter.Use(middleware.SystemPerformanceCheck())
	registerMjRouterGroup(relayMjRouter)

	relayMjModeRouter := router.Group("/:mode/mj")
	relayMjModeRouter.Use(middleware.RouteTag("relay"))
	relayMjModeRouter.Use(middleware.SystemPerformanceCheck())
	registerMjRouterGroup(relayMjModeRouter)
	//relayMjRouter.Use()

	relaySunoRouter := router.Group("/suno")
	relaySunoRouter.Use(middleware.RouteTag("relay"))
	relaySunoRouter.Use(middleware.SystemPerformanceCheck())
	relaySunoRouter.Use(middleware.TokenAuth(), middleware.Distribute())
	{
		relaySunoRouter.POST("/submit/:action", controller.RelayTask)
		relaySunoRouter.POST("/fetch", controller.RelayTaskFetch)
		relaySunoRouter.GET("/fetch/:id", controller.RelayTaskFetch)
	}

	relayGeminiRouter := router.Group("/v1beta")
	relayGeminiRouter.Use(middleware.RouteTag("relay"))
	relayGeminiRouter.Use(middleware.SystemPerformanceCheck())
	relayGeminiRouter.Use(middleware.TokenAuth())
	relayGeminiRouter.Use(middleware.ModelRequestRateLimit())
	relayGeminiRouter.Use(middleware.Distribute())
	{
		// Gemini API 路径格式: /v1beta/models/{model_name}:{action}
		relayGeminiRouter.POST("/models/*path", func(c *gin.Context) {
			controller.Relay(c, types.RelayFormatGemini)
		})
	}
}

func registerMjRouterGroup(relayMjRouter *gin.RouterGroup) {
	relayMjRouter.GET("/image/:id", relay.RelayMidjourneyImage)
	relayMjRouter.Use(middleware.TokenAuth(), middleware.Distribute())
	{
		relayMjRouter.POST("/submit/action", controller.RelayMidjourney)
		relayMjRouter.POST("/submit/shorten", controller.RelayMidjourney)
		relayMjRouter.POST("/submit/modal", controller.RelayMidjourney)
		relayMjRouter.POST("/submit/imagine", controller.RelayMidjourney)
		relayMjRouter.POST("/submit/change", controller.RelayMidjourney)
		relayMjRouter.POST("/submit/simple-change", controller.RelayMidjourney)
		relayMjRouter.POST("/submit/describe", controller.RelayMidjourney)
		relayMjRouter.POST("/submit/blend", controller.RelayMidjourney)
		relayMjRouter.POST("/submit/edits", controller.RelayMidjourney)
		relayMjRouter.POST("/submit/video", controller.RelayMidjourney)
		//relayMjRouter.POST("/notify", controller.RelayMidjourney)
		relayMjRouter.GET("/task/:id/fetch", controller.RelayMidjourney)
		relayMjRouter.GET("/task/:id/image-seed", controller.RelayMidjourney)
		relayMjRouter.POST("/task/list-by-condition", controller.RelayMidjourney)
		relayMjRouter.POST("/insight-face/swap", controller.RelayMidjourney)
		relayMjRouter.POST("/submit/upload-discord-images", controller.RelayMidjourney)
	}
}
