package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/pprof"
	"net/netip"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/constant"
	"github.com/LIghtJUNction/api.lmm.best/controller"
	"github.com/LIghtJUNction/api.lmm.best/i18n"
	"github.com/LIghtJUNction/api.lmm.best/internal/appcli"
	"github.com/LIghtJUNction/api.lmm.best/logger"
	"github.com/LIghtJUNction/api.lmm.best/middleware"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/oauth"
	perfmetrics "github.com/LIghtJUNction/api.lmm.best/pkg/perf_metrics"
	"github.com/LIghtJUNction/api.lmm.best/pkg/wsmanager"
	"github.com/LIghtJUNction/api.lmm.best/relay"
	kitutil "github.com/LIghtJUNction/api.lmm.best/relaykit/relayconvert/kitutil"
	"github.com/LIghtJUNction/api.lmm.best/router"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/LIghtJUNction/api.lmm.best/service/authz"
	_ "github.com/LIghtJUNction/api.lmm.best/setting/performance_setting"
	"github.com/LIghtJUNction/api.lmm.best/setting/ratio_setting"
	"github.com/LIghtJUNction/api.lmm.best/setting/system_setting"

	"github.com/bytedance/gopkg/util/gopool"
	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
)

func main() {
	dispatch := appcli.Dispatch(os.Args[1:], common.Version, os.Stdout, os.Stderr)
	switch dispatch.Mode {
	case appcli.ModeServe:
		// common.InitEnv owns the server flag set. Remove the optional serve word so
		// `lmm-api serve --port ...` and `lmm-api --port ...` share one parser.
		os.Args = append([]string{os.Args[0]}, dispatch.ServeArgs...)
		runServer()
	case appcli.ModeMigrateApply:
		runMigrationCommand(model.DBMigrationModeApply)
	case appcli.ModeMigrateVerify:
		runMigrationCommand(model.DBMigrationModeVerify)
	default:
		if dispatch.ExitCode != appcli.ExitOK {
			os.Exit(dispatch.ExitCode)
		}
	}
}

func runMigrationCommand(mode model.DBMigrationMode) {
	if err := os.Setenv("LMM_DB_MIGRATION_MODE", string(mode)); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "%s migrate: set migration mode: %v\n", appcli.ProgramName, err)
		os.Exit(appcli.ExitError)
	}
	// Do not pass the migration subcommand to the server flag parser.
	os.Args = []string{os.Args[0]}
	if err := InitResources(); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "%s migrate --%s: %v\n", appcli.ProgramName, mode, err)
		os.Exit(appcli.ExitError)
	}
	if err := errors.Join(common.CloseRedis(), model.CloseDB()); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "%s migrate --%s: close resources: %v\n", appcli.ProgramName, mode, err)
		os.Exit(appcli.ExitError)
	}
	_, _ = fmt.Fprintf(os.Stdout, "database migration %s completed\n", mode)
}

func runServer() {
	startTime := time.Now()
	loops := newRuntimeLoops(context.Background())
	resourcesOpen := false
	defer func() {
		model.MarkCacheReadinessUnavailable()
		loops.Stop()
		waitCtx, cancel := context.WithTimeout(context.Background(), loopShutdownTimeout)
		defer cancel()
		_ = errors.Join(loops.Wait(waitCtx), model.WaitForCacheWarm(waitCtx))
		if resourcesOpen {
			if err := errors.Join(common.CloseRedis(), model.CloseDB()); err != nil {
				common.SysError("failed to close resources: " + err.Error())
			}
		}
	}()
	kitutil.SetLogging(common.SysLog, func(message string) {
		logger.LogError(context.TODO(), message)
	})
	kitutil.SetSystemErrorLogging(common.SysError)

	err := InitResources()
	if err != nil {
		_ = errors.Join(common.CloseRedis(), model.CloseDB())
		common.FatalLog("failed to initialize resources: " + err.Error())
		return
	}
	resourcesOpen = true

	common.SysLog(common.SystemName + " " + common.Version + " started")
	if os.Getenv("GIN_MODE") != "debug" {
		gin.SetMode(gin.ReleaseMode)
	}
	if common.DebugEnabled {
		common.SysLog("running in debug mode")
	}

	kitutil.Debug.Store(common.DebugEnabled)

	multiSlotRuntime := strings.TrimSpace(common.APIInstanceSlot) != ""
	if multiSlotRuntime {
		if !common.UsingMainDatabase(common.DatabaseTypePostgreSQL) || model.DB == nil {
			common.FatalLog("LMM_API_INSTANCE_SLOT requires an initialized PostgreSQL primary database")
			return
		}
		if _, err := model.DB.DB(); err != nil {
			common.FatalLog("LMM_API_INSTANCE_SLOT cannot open the PostgreSQL leadership pool: " + err.Error())
			return
		}
	}
	runCriticalLoop := func(label string, run func(context.Context) error) func(context.Context) {
		return func(ctx context.Context) {
			if err := run(ctx); err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
				common.SysError(label + " stopped: " + err.Error())
				model.MarkCacheReadinessUnavailable()
			}
		}
	}

	loops.Go(perfmetrics.Run)
	loops.Go(service.RunDynamicPricingTicker)
	loops.Go(common.RunSystemMonitor)
	loops.Go(service.RunAuthArtifactCleanup)

	if common.RedisEnabled {
		// for compatibility with old versions
		common.MemoryCacheEnabled = true
	}
	if common.MemoryCacheEnabled {
		common.SysLog("memory cache enabled")
		common.SysLog(fmt.Sprintf("sync frequency: %d seconds", common.SyncFrequency))
		loops.Go(func(ctx context.Context) { model.SyncChannelCacheContext(ctx, common.SyncFrequency) })
	}
	// Keep the Valkey subscriber inside the lifecycle registry so shutdown waits
	// for it before closing the shared client.
	loops.Go(wsmanager.RunSubscriber)

	// Perform one bounded synchronous warm before the server starts accepting
	// traffic. Transient failure keeps the process alive but readiness remains
	// false; probes trigger a serialized retry.
	if err := model.WarmCaches(); err != nil {
		common.SysLog(fmt.Sprintf("initial cache warm failed; service is not ready: %v", err))
		model.EnsureCachesWarmAsync()
	}

	// 热更新配置
	loops.Go(func(ctx context.Context) { model.SyncOptionsContext(ctx, common.SyncFrequency) })

	// 周期性重载授权策略，保证多节点/多 master 部署下权限变更能传播到每个实例
	loops.Go(func(ctx context.Context) { authz.StartPolicySyncContext(ctx, common.SyncFrequency) })

	// 数据看板
	loops.Go(model.UpdateQuotaDataContext)

	if os.Getenv("CHANNEL_UPDATE_FREQUENCY") != "" {
		frequency, err := strconv.Atoi(os.Getenv("CHANNEL_UPDATE_FREQUENCY"))
		if err != nil || frequency <= 0 {
			common.FatalLog("failed to parse positive CHANNEL_UPDATE_FREQUENCY: " + os.Getenv("CHANNEL_UPDATE_FREQUENCY"))
			return
		}
		if multiSlotRuntime {
			loops.Go(runCriticalLoop("automatic channel balance leadership", func(ctx context.Context) error {
				return controller.RunAutomaticChannelBalanceUpdateWithLeadership(ctx, frequency)
			}))
		} else {
			loops.Go(func(ctx context.Context) { controller.AutomaticallyUpdateChannelsContext(ctx, frequency) })
		}
	}

	// Codex credential auto-refresh check every 10 minutes, refresh when expires within 1 day.
	if multiSlotRuntime {
		loops.Go(runCriticalLoop("Codex credential refresh leadership", service.RunCodexCredentialAutoRefreshTaskWithLeadership))
	} else {
		loops.Go(service.RunCodexCredentialAutoRefreshTask)
	}

	// Subscription quota reset task (daily/weekly/monthly/custom).
	if multiSlotRuntime {
		loops.Go(runCriticalLoop("subscription maintenance leadership", service.RunSubscriptionMaintenanceScanWithLeadership))
	} else {
		loops.Go(service.RunSubscriptionQuotaResetTask)
	}

	// Report this process as a system instance so the System Info page can show
	// all currently alive nodes in multi-instance deployments.
	loops.Go(service.RunSystemInstanceReporter)

	// Wire task polling adaptor factory (breaks service -> relay import cycle).
	// Must run before the system task runner starts: the async_task_poll handler
	// calls service.RunTaskPollingOnce, which needs this factory set.
	service.GetTaskAdaptorFunc = func(platform constant.TaskPlatform) service.TaskPollingAdaptor {
		a := relay.GetTaskAdaptor(platform)
		if a == nil {
			return nil
		}
		return a
	}

	// Register the periodic channel test, upstream model update, and async task
	// polling (Midjourney / Suno / video) jobs as scheduled system tasks
	// (DB-lease dedup across masters + run history), then start the runner that
	// schedules and executes them. Master-only execution and the UpdateTask
	// switch are enforced inside the runner and each handler's Enabled().
	controller.RegisterScheduledSystemTasks()
	loops.Go(service.RunSystemTaskRunner)

	if os.Getenv("BATCH_UPDATE_ENABLED") == "true" {
		common.BatchUpdateEnabled = true
		common.SysLog("batch update enabled with interval " + strconv.Itoa(common.BatchUpdateInterval) + "s")
		loops.Go(model.RunBatchUpdater)
	}

	if os.Getenv("ENABLE_PPROF") == "true" {
		pprofAddress, err := pprofListenAddress(
			os.Getenv("PPROF_BIND_ADDRESS"),
			os.Getenv("PPROF_PORT"),
		)
		if err != nil {
			common.FatalLog("failed to configure pprof listen address: " + err.Error())
			return
		}
		gopool.Go(func() {
			profileServer := &http.Server{
				Addr:              pprofAddress,
				Handler:           pprofHandler(),
				ReadHeaderTimeout: 5 * time.Second,
			}
			if err := profileServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				common.SysError(fmt.Sprintf("pprof server stopped: %v", err))
			}
		})
		go common.Monitor()
		common.SysLog("pprof enabled on " + pprofAddress)
	}

	err = common.StartPyroScope()
	if err != nil {
		common.SysError(fmt.Sprintf("start pyroscope error : %v", err))
	}

	// Initialize HTTP server
	server := gin.New()
	if err := middleware.ConfigureTrustedProxies(server); err != nil {
		common.FatalLog("failed to configure trusted proxies: " + err.Error())
		return
	}
	server.Use(gin.CustomRecovery(func(c *gin.Context, err any) {
		common.SysLog(fmt.Sprintf("panic detected: %v", err))
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": gin.H{
				"message": "The server encountered an internal error. Please try again or contact support.",
				"type":    "new_api_panic",
			},
		})
	}))
	// This will cause SSE not to work!!!
	//server.Use(gzip.Gzip(gzip.DefaultCompression))
	server.Use(middleware.RequestId())
	server.Use(middleware.Version())
	server.Use(middleware.I18n())
	server.Use(middleware.AntiRelayAccess())
	middleware.SetUpLogger(server)
	// 设置路由
	if err := router.SetRouter(server); err != nil {
		common.FatalLog("failed to configure routes: " + err.Error())
		return
	}
	port := resolvePort(os.Getenv("PORT"), os.Getenv("LMM_API_PORT"), *common.Port)
	listenAddress, err := buildListenAddress(os.Getenv("LMM_API_BIND_ADDRESS"), port)
	if err != nil {
		common.FatalLog("failed to configure HTTP listen address: " + err.Error())
		return
	}
	if err := edgeAccessBindPolicy(os.Getenv("LMM_API_BIND_ADDRESS"), listenAddress); err != nil {
		common.FatalLog("failed to configure IP access routing: " + err.Error())
		return
	}
	localAcceptance, err := localAcceptancePolicy(
		os.Getenv("LMM_LOCAL_ACCEPTANCE"),
		os.Getenv("LMM_API_BIND_ADDRESS"),
		listenAddress,
	)
	if err != nil {
		common.FatalLog("failed to configure local acceptance: " + err.Error())
		return
	}
	model.SetLocalAcceptanceDeveloperAccess(localAcceptance)

	srv := &http.Server{
		Addr:    listenAddress,
		Handler: server,
	}

	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			common.FatalLog("failed to start HTTP server: " + err.Error())
		}
	}()

	time.Sleep(100 * time.Millisecond)

	common.LogStartupSuccess(startTime, port)

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	sig := <-quit
	common.SysLog(fmt.Sprintf("received signal: %v, shutting down...", sig))

	shutdownTimeout := configuredShutdownTimeout(common.GetEnvOrDefault("SHUTDOWN_TIMEOUT_SECONDS", int(httpShutdownTimeout/time.Second)))
	err = shutdownRuntime(shutdownSteps{
		markUnready:  model.MarkCacheReadinessUnavailable,
		stopLoops:    loops.Stop,
		drainSockets: wsmanager.DrainAll,
		shutdownHTTP: func(ctx context.Context) error {
			if shutdownErr := srv.Shutdown(ctx); shutdownErr != nil {
				// A timed-out graceful drain must not keep handlers mutating the
				// in-memory stores while the final flush is running.
				return errors.Join(shutdownErr, srv.Close())
			}
			return nil
		},
		waitLoops: func(ctx context.Context) error {
			return errors.Join(loops.Wait(ctx), model.WaitForCacheWarm(ctx))
		},
		flushQuota: func() {
			if common.DataExportEnabled {
				model.SaveQuotaDataCache()
			}
		},
		flushBatch:  model.FlushBatchUpdates,
		flushPerf:   perfmetrics.Flush,
		closeValkey: common.CloseRedis,
		closeDB:     model.CloseDB,
	}, shutdownTimeout, loopShutdownTimeout)
	resourcesOpen = false
	if err != nil {
		common.SysError("shutdown completed with errors: " + err.Error())
	}
	common.SysLog("server exited")
}

func buildListenAddress(bindAddress, port string) (string, error) {
	if bindAddress == "" {
		// Keep the application private by default. Production IP and region
		// routing runs at Nginx; a public listener would let callers bypass the
		// edge-owned client IP, destination port, and GeoIP metadata.
		return net.JoinHostPort("127.0.0.1", port), nil
	}
	bindAddress = strings.TrimSpace(bindAddress)
	if bindAddress == "" {
		return "", fmt.Errorf("LMM_API_BIND_ADDRESS must not contain only whitespace")
	}
	if net.ParseIP(bindAddress) == nil {
		return "", fmt.Errorf("LMM_API_BIND_ADDRESS must be an IPv4 or IPv6 address without a port: %q", bindAddress)
	}

	return net.JoinHostPort(bindAddress, port), nil
}

// edgeAccessBindPolicy keeps the application private so callers cannot bypass
// Nginx's trusted client-IP, destination-port, and GeoIP metadata.
func edgeAccessBindPolicy(configuredBindAddress, listenAddress string) error {
	configured := strings.TrimSpace(configuredBindAddress)
	if configured != "" && !isExactLoopbackHost(configured) {
		return fmt.Errorf("IP access routing requires LMM_API_BIND_ADDRESS to be exactly 127.0.0.1 or ::1")
	}
	host, _, err := net.SplitHostPort(listenAddress)
	if err != nil || !isExactLoopbackHost(host) {
		return fmt.Errorf("IP access routing requires the final HTTP listen address to be loopback")
	}
	return nil
}

func localAcceptancePolicy(flagValue, configuredBindAddress, listenAddress string) (bool, error) {
	if flagValue != "true" {
		return false, nil
	}

	if !isExactLoopbackHost(configuredBindAddress) {
		return false, fmt.Errorf("LMM_LOCAL_ACCEPTANCE requires LMM_API_BIND_ADDRESS to be exactly 127.0.0.1 or ::1")
	}

	listenHost, listenPort, err := net.SplitHostPort(listenAddress)
	if err != nil {
		return false, fmt.Errorf("LMM_LOCAL_ACCEPTANCE requires an unambiguous loopback listen address: %w", err)
	}
	if !isExactLoopbackHost(listenHost) {
		return false, fmt.Errorf("LMM_LOCAL_ACCEPTANCE requires the final listen host to be exactly 127.0.0.1 or ::1")
	}
	port, err := strconv.Atoi(listenPort)
	if err != nil || port < 1 || port > 65535 {
		return false, fmt.Errorf("LMM_LOCAL_ACCEPTANCE requires a numeric final listen port between 1 and 65535")
	}

	return true, nil
}

func isExactLoopbackHost(host string) bool {
	address, err := netip.ParseAddr(host)
	if err != nil || address.Is4In6() || address.Zone() != "" {
		return false
	}
	return address == netip.MustParseAddr("127.0.0.1") || address == netip.IPv6Loopback()
}

func InitResources() (returnErr error) {
	// Initialize resources here if needed
	// This is a placeholder function for future resource initialization
	err := godotenv.Load(".env")
	if err != nil {
		if common.DebugEnabled {
			common.SysLog("No .env file found, using default environment variables. If needed, please create a .env file and set the relevant variables.")
		}
	}

	// 加载环境变量
	if err := common.InitializeEnvironment(); err != nil {
		return fmt.Errorf("initialize environment: %w", err)
	}
	if err := system_setting.InitServerAddressFromEnv(); err != nil {
		return fmt.Errorf("failed to configure server address: %w", err)
	}

	if err := logger.InitializeLogger(); err != nil {
		return fmt.Errorf("initialize logger: %w", err)
	}

	// Initialize model settings
	ratio_setting.InitRatioSettings()

	service.InitHttpClient()

	service.InitTokenEncoders()

	// Initialize SQL Database
	migrationSession, err := model.InitDBWithMigrationSession()
	if err != nil {
		return fmt.Errorf("failed to initialize database: %w", err)
	}
	defer func() {
		returnErr = errors.Join(returnErr, migrationSession.Close())
	}()
	if err = authz.InitForStartup(model.DB, migrationSession.Applies()); err != nil {
		return fmt.Errorf("failed to initialize authorization: %w", err)
	}

	if err = model.CheckSetupForStartup(migrationSession.Applies()); err != nil {
		return fmt.Errorf("verify setup state: %w", err)
	}

	// Initialize options, should after model.InitDB()
	if common.IsMasterNode && migrationSession.Applies() {
		if err := model.MigrateRetiredFrontendOptions(); err != nil {
			common.SysError("failed to migrate retired frontend options: " + err.Error())
		}
	}
	model.InitOptionMap()

	// 清理旧的磁盘缓存文件
	common.CleanupOldCacheFiles()

	// Initialize SQL Database
	err = model.InitLogDB(migrationSession)
	if err != nil {
		return err
	}

	// Initialize Redis
	err = common.InitRedisClient()
	if err != nil {
		return err
	}

	// Initialize i18n
	err = i18n.Init()
	if err != nil {
		common.SysError("failed to initialize i18n: " + err.Error())
		// Don't return error, i18n is not critical
	} else {
		common.SysLog("i18n initialized with languages: " + strings.Join(i18n.SupportedLanguages(), ", "))
	}
	// Register user language loader for lazy loading
	i18n.SetUserLangLoader(model.GetUserLanguage)

	// Load custom OAuth providers from database
	err = oauth.LoadCustomProviders()
	if err != nil {
		common.SysError("failed to load custom OAuth providers: " + err.Error())
		// Don't return error, custom OAuth is not critical
	}

	return nil
}

const (
	defaultPprofBindAddress = "127.0.0.1"
	defaultPprofPort        = "8005"
)

func resolvePort(primary, compatibility string, fallback int) string {
	for _, value := range []string{primary, compatibility} {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return strconv.Itoa(fallback)
}

func pprofListenAddress(bindAddress, port string) (string, error) {
	if strings.TrimSpace(bindAddress) == "" {
		bindAddress = defaultPprofBindAddress
	}
	bindAddress = strings.TrimSpace(bindAddress)
	if !isExactLoopbackHost(bindAddress) {
		return "", fmt.Errorf("PPROF_BIND_ADDRESS must be exactly 127.0.0.1 or ::1")
	}
	if strings.TrimSpace(port) == "" {
		port = defaultPprofPort
	}
	port = strings.TrimSpace(port)
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 {
		return "", fmt.Errorf("PPROF_PORT must be a numeric TCP port between 1 and 65535")
	}
	return net.JoinHostPort(bindAddress, port), nil
}

func pprofHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	return mux
}
