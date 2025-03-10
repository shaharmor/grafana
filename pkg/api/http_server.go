package api

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"

	"github.com/grafana/grafana/pkg/api/routing"
	httpstatic "github.com/grafana/grafana/pkg/api/static"
	"github.com/grafana/grafana/pkg/bus"
	"github.com/grafana/grafana/pkg/components/simplejson"
	"github.com/grafana/grafana/pkg/infra/localcache"
	"github.com/grafana/grafana/pkg/infra/log"
	"github.com/grafana/grafana/pkg/infra/remotecache"
	"github.com/grafana/grafana/pkg/infra/tracing"
	"github.com/grafana/grafana/pkg/login/social"
	"github.com/grafana/grafana/pkg/middleware"
	"github.com/grafana/grafana/pkg/models"
	"github.com/grafana/grafana/pkg/plugins"
	"github.com/grafana/grafana/pkg/plugins/plugincontext"
	"github.com/grafana/grafana/pkg/services/accesscontrol"
	acmiddleware "github.com/grafana/grafana/pkg/services/accesscontrol/middleware"
	"github.com/grafana/grafana/pkg/services/accesscontrol/resourcepermissions"
	"github.com/grafana/grafana/pkg/services/accesscontrol/resourceservices"
	"github.com/grafana/grafana/pkg/services/alerting"
	"github.com/grafana/grafana/pkg/services/cleanup"
	"github.com/grafana/grafana/pkg/services/contexthandler"
	"github.com/grafana/grafana/pkg/services/datasourceproxy"
	"github.com/grafana/grafana/pkg/services/datasources"
	"github.com/grafana/grafana/pkg/services/encryption"
	"github.com/grafana/grafana/pkg/services/featuremgmt"
	"github.com/grafana/grafana/pkg/services/hooks"
	"github.com/grafana/grafana/pkg/services/libraryelements"
	"github.com/grafana/grafana/pkg/services/librarypanels"
	"github.com/grafana/grafana/pkg/services/live"
	"github.com/grafana/grafana/pkg/services/live/pushhttp"
	"github.com/grafana/grafana/pkg/services/login"
	"github.com/grafana/grafana/pkg/services/login/authinfoservice"
	"github.com/grafana/grafana/pkg/services/ngalert"
	"github.com/grafana/grafana/pkg/services/provisioning"
	"github.com/grafana/grafana/pkg/services/query"
	"github.com/grafana/grafana/pkg/services/queryhistory"
	"github.com/grafana/grafana/pkg/services/quota"
	"github.com/grafana/grafana/pkg/services/rendering"
	"github.com/grafana/grafana/pkg/services/schemaloader"
	"github.com/grafana/grafana/pkg/services/search"
	"github.com/grafana/grafana/pkg/services/searchusers"
	"github.com/grafana/grafana/pkg/services/secrets"
	"github.com/grafana/grafana/pkg/services/serviceaccounts"
	"github.com/grafana/grafana/pkg/services/shorturls"
	"github.com/grafana/grafana/pkg/services/sqlstore"
	"github.com/grafana/grafana/pkg/services/teamguardian"
	"github.com/grafana/grafana/pkg/services/thumbs"
	"github.com/grafana/grafana/pkg/services/updatechecker"
	"github.com/grafana/grafana/pkg/setting"
	"github.com/grafana/grafana/pkg/util/errutil"
	"github.com/grafana/grafana/pkg/web"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type HTTPServer struct {
	log              log.Logger
	web              *web.Mux
	context          context.Context
	httpSrv          *http.Server
	middlewares      []web.Handler
	namedMiddlewares []routing.RegisterNamedMiddleware

	PluginContextProvider     *plugincontext.Provider
	RouteRegister             routing.RouteRegister
	Bus                       bus.Bus
	RenderService             rendering.Service
	Cfg                       *setting.Cfg
	Features                  *featuremgmt.FeatureManager
	SettingsProvider          setting.Provider
	HooksService              *hooks.HooksService
	CacheService              *localcache.CacheService
	DataSourceCache           datasources.CacheService
	AuthTokenService          models.UserTokenService
	QuotaService              *quota.QuotaService
	RemoteCacheService        *remotecache.RemoteCache
	ProvisioningService       provisioning.ProvisioningService
	Login                     login.Service
	License                   models.Licensing
	AccessControl             accesscontrol.AccessControl
	DataProxy                 *datasourceproxy.DataSourceProxyService
	PluginRequestValidator    models.PluginRequestValidator
	pluginClient              plugins.Client
	pluginStore               plugins.Store
	pluginDashboardManager    plugins.PluginDashboardManager
	pluginStaticRouteResolver plugins.StaticRouteResolver
	pluginErrorResolver       plugins.ErrorResolver
	SearchService             *search.SearchService
	ShortURLService           shorturls.Service
	QueryHistoryService       queryhistory.Service
	Live                      *live.GrafanaLive
	LivePushGateway           *pushhttp.Gateway
	ThumbService              thumbs.Service
	ContextHandler            *contexthandler.ContextHandler
	SQLStore                  *sqlstore.SQLStore
	AlertEngine               *alerting.AlertEngine
	LoadSchemaService         *schemaloader.SchemaLoaderService
	AlertNG                   *ngalert.AlertNG
	LibraryPanelService       librarypanels.Service
	LibraryElementService     libraryelements.Service
	SocialService             social.Service
	Listener                  net.Listener
	EncryptionService         encryption.Internal
	SecretsService            secrets.Service
	DataSourcesService        *datasources.Service
	cleanUpService            *cleanup.CleanUpService
	tracer                    tracing.Tracer
	updateChecker             *updatechecker.Service
	searchUsersService        searchusers.Service
	teamGuardian              teamguardian.TeamGuardian
	queryDataService          *query.Service
	serviceAccountsService    serviceaccounts.Service
	authInfoService           authinfoservice.Service
	TeamPermissionsService    *resourcepermissions.Service
}

type ServerOptions struct {
	Listener net.Listener
}

func ProvideHTTPServer(opts ServerOptions, cfg *setting.Cfg, routeRegister routing.RouteRegister, bus bus.Bus,
	renderService rendering.Service, licensing models.Licensing, hooksService *hooks.HooksService,
	cacheService *localcache.CacheService, sqlStore *sqlstore.SQLStore, alertEngine *alerting.AlertEngine,
	pluginRequestValidator models.PluginRequestValidator, pluginStaticRouteResolver plugins.StaticRouteResolver,
	pluginDashboardManager plugins.PluginDashboardManager, pluginStore plugins.Store, pluginClient plugins.Client,
	pluginErrorResolver plugins.ErrorResolver, settingsProvider setting.Provider,
	dataSourceCache datasources.CacheService, userTokenService models.UserTokenService,
	cleanUpService *cleanup.CleanUpService, shortURLService shorturls.Service, queryHistoryService queryhistory.Service,
	thumbService thumbs.Service, remoteCache *remotecache.RemoteCache, provisioningService provisioning.ProvisioningService,
	loginService login.Service, accessControl accesscontrol.AccessControl,
	dataSourceProxy *datasourceproxy.DataSourceProxyService, searchService *search.SearchService,
	live *live.GrafanaLive, livePushGateway *pushhttp.Gateway, plugCtxProvider *plugincontext.Provider,
	contextHandler *contexthandler.ContextHandler, features *featuremgmt.FeatureManager,
	schemaService *schemaloader.SchemaLoaderService, alertNG *ngalert.AlertNG,
	libraryPanelService librarypanels.Service, libraryElementService libraryelements.Service,
	quotaService *quota.QuotaService, socialService social.Service, tracer tracing.Tracer,
	encryptionService encryption.Internal, updateChecker *updatechecker.Service, searchUsersService searchusers.Service,
	dataSourcesService *datasources.Service, secretsService secrets.Service, queryDataService *query.Service,
	teamGuardian teamguardian.TeamGuardian, serviceaccountsService serviceaccounts.Service,
	authInfoService authinfoservice.Service, resourcePermissionServices *resourceservices.ResourceServices) (*HTTPServer, error) {
	web.Env = cfg.Env
	m := web.New()

	hs := &HTTPServer{
		Cfg:                       cfg,
		RouteRegister:             routeRegister,
		Bus:                       bus,
		RenderService:             renderService,
		License:                   licensing,
		HooksService:              hooksService,
		CacheService:              cacheService,
		SQLStore:                  sqlStore,
		AlertEngine:               alertEngine,
		PluginRequestValidator:    pluginRequestValidator,
		pluginClient:              pluginClient,
		pluginStore:               pluginStore,
		pluginStaticRouteResolver: pluginStaticRouteResolver,
		pluginDashboardManager:    pluginDashboardManager,
		pluginErrorResolver:       pluginErrorResolver,
		updateChecker:             updateChecker,
		SettingsProvider:          settingsProvider,
		DataSourceCache:           dataSourceCache,
		AuthTokenService:          userTokenService,
		cleanUpService:            cleanUpService,
		ShortURLService:           shortURLService,
		QueryHistoryService:       queryHistoryService,
		Features:                  features,
		ThumbService:              thumbService,
		RemoteCacheService:        remoteCache,
		ProvisioningService:       provisioningService,
		Login:                     loginService,
		AccessControl:             accessControl,
		DataProxy:                 dataSourceProxy,
		SearchService:             searchService,
		Live:                      live,
		LivePushGateway:           livePushGateway,
		PluginContextProvider:     plugCtxProvider,
		ContextHandler:            contextHandler,
		LoadSchemaService:         schemaService,
		AlertNG:                   alertNG,
		LibraryPanelService:       libraryPanelService,
		LibraryElementService:     libraryElementService,
		QuotaService:              quotaService,
		tracer:                    tracer,
		log:                       log.New("http.server"),
		web:                       m,
		Listener:                  opts.Listener,
		SocialService:             socialService,
		EncryptionService:         encryptionService,
		SecretsService:            secretsService,
		DataSourcesService:        dataSourcesService,
		searchUsersService:        searchUsersService,
		teamGuardian:              teamGuardian,
		queryDataService:          queryDataService,
		serviceAccountsService:    serviceaccountsService,
		authInfoService:           authInfoService,
		TeamPermissionsService:    resourcePermissionServices.GetTeamService(),
	}
	if hs.Listener != nil {
		hs.log.Debug("Using provided listener")
	}
	hs.registerRoutes()

	if err := hs.declareFixedRoles(); err != nil {
		return nil, err
	}
	return hs, nil
}

func (hs *HTTPServer) AddMiddleware(middleware web.Handler) {
	hs.middlewares = append(hs.middlewares, middleware)
}

func (hs *HTTPServer) AddNamedMiddleware(middleware routing.RegisterNamedMiddleware) {
	hs.namedMiddlewares = append(hs.namedMiddlewares, middleware)
}

func (hs *HTTPServer) Run(ctx context.Context) error {
	hs.context = ctx

	hs.applyRoutes()

	// Remove any square brackets enclosing IPv6 addresses, a format we support for backwards compatibility
	host := strings.TrimSuffix(strings.TrimPrefix(hs.Cfg.HTTPAddr, "["), "]")
	hs.httpSrv = &http.Server{
		Addr:        net.JoinHostPort(host, hs.Cfg.HTTPPort),
		Handler:     hs.web,
		ReadTimeout: hs.Cfg.ReadTimeout,
	}
	switch hs.Cfg.Protocol {
	case setting.HTTP2Scheme:
		if err := hs.configureHttp2(); err != nil {
			return err
		}
	case setting.HTTPSScheme:
		if err := hs.configureHttps(); err != nil {
			return err
		}
	default:
	}

	listener, err := hs.getListener()
	if err != nil {
		return err
	}

	hs.log.Info("HTTP Server Listen", "address", listener.Addr().String(), "protocol",
		hs.Cfg.Protocol, "subUrl", hs.Cfg.AppSubURL, "socket", hs.Cfg.SocketPath)

	var wg sync.WaitGroup
	wg.Add(1)

	// handle http shutdown on server context done
	go func() {
		defer wg.Done()

		<-ctx.Done()
		if err := hs.httpSrv.Shutdown(context.Background()); err != nil {
			hs.log.Error("Failed to shutdown server", "error", err)
		}
	}()

	switch hs.Cfg.Protocol {
	case setting.HTTPScheme, setting.SocketScheme:
		if err := hs.httpSrv.Serve(listener); err != nil {
			if errors.Is(err, http.ErrServerClosed) {
				hs.log.Debug("server was shutdown gracefully")
				return nil
			}
			return err
		}
	case setting.HTTP2Scheme, setting.HTTPSScheme:
		if err := hs.httpSrv.ServeTLS(listener, hs.Cfg.CertFile, hs.Cfg.KeyFile); err != nil {
			if errors.Is(err, http.ErrServerClosed) {
				hs.log.Debug("server was shutdown gracefully")
				return nil
			}
			return err
		}
	default:
		panic(fmt.Sprintf("Unhandled protocol %q", hs.Cfg.Protocol))
	}

	wg.Wait()

	return nil
}

func (hs *HTTPServer) getListener() (net.Listener, error) {
	if hs.Listener != nil {
		return hs.Listener, nil
	}

	switch hs.Cfg.Protocol {
	case setting.HTTPScheme, setting.HTTPSScheme, setting.HTTP2Scheme:
		listener, err := net.Listen("tcp", hs.httpSrv.Addr)
		if err != nil {
			return nil, errutil.Wrapf(err, "failed to open listener on address %s", hs.httpSrv.Addr)
		}
		return listener, nil
	case setting.SocketScheme:
		listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: hs.Cfg.SocketPath, Net: "unix"})
		if err != nil {
			return nil, errutil.Wrapf(err, "failed to open listener for socket %s", hs.Cfg.SocketPath)
		}

		// Make socket writable by group
		// nolint:gosec
		if err := os.Chmod(hs.Cfg.SocketPath, 0660); err != nil {
			return nil, errutil.Wrapf(err, "failed to change socket permissions")
		}

		return listener, nil
	default:
		hs.log.Error("Invalid protocol", "protocol", hs.Cfg.Protocol)
		return nil, fmt.Errorf("invalid protocol %q", hs.Cfg.Protocol)
	}
}

func (hs *HTTPServer) configureHttps() error {
	if hs.Cfg.CertFile == "" {
		return fmt.Errorf("cert_file cannot be empty when using HTTPS")
	}

	if hs.Cfg.KeyFile == "" {
		return fmt.Errorf("cert_key cannot be empty when using HTTPS")
	}

	if _, err := os.Stat(hs.Cfg.CertFile); os.IsNotExist(err) {
		return fmt.Errorf(`cannot find SSL cert_file at %q`, hs.Cfg.CertFile)
	}

	if _, err := os.Stat(hs.Cfg.KeyFile); os.IsNotExist(err) {
		return fmt.Errorf(`cannot find SSL key_file at %q`, hs.Cfg.KeyFile)
	}

	tlsCfg := &tls.Config{
		MinVersion:               tls.VersionTLS12,
		PreferServerCipherSuites: true,
		CipherSuites: []uint16{
			tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA,
			tls.TLS_ECDHE_ECDSA_WITH_AES_256_CBC_SHA,
			tls.TLS_ECDHE_RSA_WITH_AES_256_CBC_SHA,
			tls.TLS_RSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_RSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_RSA_WITH_AES_128_CBC_SHA,
			tls.TLS_RSA_WITH_AES_256_CBC_SHA,
		},
	}

	hs.httpSrv.TLSConfig = tlsCfg
	hs.httpSrv.TLSNextProto = make(map[string]func(*http.Server, *tls.Conn, http.Handler))

	return nil
}

func (hs *HTTPServer) configureHttp2() error {
	if hs.Cfg.CertFile == "" {
		return fmt.Errorf("cert_file cannot be empty when using HTTP2")
	}

	if hs.Cfg.KeyFile == "" {
		return fmt.Errorf("cert_key cannot be empty when using HTTP2")
	}

	if _, err := os.Stat(hs.Cfg.CertFile); os.IsNotExist(err) {
		return fmt.Errorf(`cannot find SSL cert_file at %q`, hs.Cfg.CertFile)
	}

	if _, err := os.Stat(hs.Cfg.KeyFile); os.IsNotExist(err) {
		return fmt.Errorf(`cannot find SSL key_file at %q`, hs.Cfg.KeyFile)
	}

	tlsCfg := &tls.Config{
		MinVersion:               tls.VersionTLS12,
		PreferServerCipherSuites: true,
		CipherSuites: []uint16{
			tls.TLS_CHACHA20_POLY1305_SHA256,
			tls.TLS_AES_128_GCM_SHA256,
			tls.TLS_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305,
			tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305,
		},
		NextProtos: []string{"h2", "http/1.1"},
	}

	hs.httpSrv.TLSConfig = tlsCfg

	return nil
}

func (hs *HTTPServer) applyRoutes() {
	// start with middlewares & static routes
	hs.addMiddlewaresAndStaticRoutes()
	// then add view routes & api routes
	hs.RouteRegister.Register(hs.web, hs.namedMiddlewares...)
	// then custom app proxy routes
	hs.initAppPluginRoutes(hs.web)
	// lastly not found route
	hs.web.NotFound(middleware.ReqSignedIn, hs.NotFoundHandler)
}

func (hs *HTTPServer) addMiddlewaresAndStaticRoutes() {
	m := hs.web

	m.Use(middleware.RequestTracing(hs.tracer))

	m.Use(middleware.Logger(hs.Cfg))

	if hs.Cfg.EnableGzip {
		m.UseMiddleware(middleware.Gziper())
	}

	m.Use(middleware.Recovery(hs.Cfg))

	hs.mapStatic(m, hs.Cfg.StaticRootPath, "build", "public/build")
	hs.mapStatic(m, hs.Cfg.StaticRootPath, "", "public")
	hs.mapStatic(m, hs.Cfg.StaticRootPath, "robots.txt", "robots.txt")

	if hs.Cfg.ImageUploadProvider == "local" {
		hs.mapStatic(m, hs.Cfg.ImagesDir, "", "/public/img/attachments")
	}

	m.Use(middleware.AddDefaultResponseHeaders(hs.Cfg))

	if hs.Cfg.ServeFromSubPath && hs.Cfg.AppSubURL != "" {
		m.SetURLPrefix(hs.Cfg.AppSubURL)
	}

	m.UseMiddleware(web.Renderer(filepath.Join(hs.Cfg.StaticRootPath, "views"), "[[", "]]"))

	// These endpoints are used for monitoring the Grafana instance
	// and should not be redirected or rejected.
	m.Use(hs.healthzHandler)
	m.Use(hs.apiHealthHandler)
	m.Use(hs.metricsEndpoint)

	m.Use(hs.ContextHandler.Middleware)
	m.Use(middleware.OrgRedirect(hs.Cfg))
	m.Use(acmiddleware.LoadPermissionsMiddleware(hs.AccessControl))

	// needs to be after context handler
	if hs.Cfg.EnforceDomain {
		m.Use(middleware.ValidateHostHeader(hs.Cfg))
	}

	m.Use(middleware.HandleNoCacheHeader)
	m.UseMiddleware(middleware.AddCSPHeader(hs.Cfg, hs.log))

	for _, mw := range hs.middlewares {
		m.Use(mw)
	}
}

func (hs *HTTPServer) metricsEndpoint(ctx *web.Context) {
	if !hs.Cfg.MetricsEndpointEnabled {
		return
	}

	if ctx.Req.Method != http.MethodGet || ctx.Req.URL.Path != "/metrics" {
		return
	}

	if hs.metricsEndpointBasicAuthEnabled() && !BasicAuthenticatedRequest(ctx.Req, hs.Cfg.MetricsEndpointBasicAuthUsername, hs.Cfg.MetricsEndpointBasicAuthPassword) {
		ctx.Resp.WriteHeader(http.StatusUnauthorized)
		return
	}

	promhttp.
		HandlerFor(prometheus.DefaultGatherer, promhttp.HandlerOpts{EnableOpenMetrics: true}).
		ServeHTTP(ctx.Resp, ctx.Req)
}

// healthzHandler always return 200 - Ok if Grafana's web server is running
func (hs *HTTPServer) healthzHandler(ctx *web.Context) {
	notHeadOrGet := ctx.Req.Method != http.MethodGet && ctx.Req.Method != http.MethodHead
	if notHeadOrGet || ctx.Req.URL.Path != "/healthz" {
		return
	}

	ctx.Resp.WriteHeader(200)
	_, err := ctx.Resp.Write([]byte("Ok"))
	if err != nil {
		hs.log.Error("could not write to response", "err", err)
	}
}

// apiHealthHandler will return ok if Grafana's web server is running and it
// can access the database. If the database cannot be accessed it will return
// http status code 503.
func (hs *HTTPServer) apiHealthHandler(ctx *web.Context) {
	notHeadOrGet := ctx.Req.Method != http.MethodGet && ctx.Req.Method != http.MethodHead
	if notHeadOrGet || ctx.Req.URL.Path != "/api/health" {
		return
	}

	data := simplejson.New()
	data.Set("database", "ok")
	if !hs.Cfg.AnonymousHideVersion {
		data.Set("version", hs.Cfg.BuildVersion)
		data.Set("commit", hs.Cfg.BuildCommit)
	}

	if !hs.databaseHealthy(ctx.Req.Context()) {
		data.Set("database", "failing")
		ctx.Resp.Header().Set("Content-Type", "application/json; charset=UTF-8")
		ctx.Resp.WriteHeader(503)
	} else {
		ctx.Resp.Header().Set("Content-Type", "application/json; charset=UTF-8")
		ctx.Resp.WriteHeader(200)
	}

	dataBytes, err := data.EncodePretty()
	if err != nil {
		hs.log.Error("Failed to encode data", "err", err)
		return
	}

	if _, err := ctx.Resp.Write(dataBytes); err != nil {
		hs.log.Error("Failed to write to response", "err", err)
	}
}

func (hs *HTTPServer) mapStatic(m *web.Mux, rootDir string, dir string, prefix string) {
	headers := func(c *web.Context) {
		c.Resp.Header().Set("Cache-Control", "public, max-age=3600")
	}

	if prefix == "public/build" {
		headers = func(c *web.Context) {
			c.Resp.Header().Set("Cache-Control", "public, max-age=31536000")
		}
	}

	if hs.Cfg.Env == setting.Dev {
		headers = func(c *web.Context) {
			c.Resp.Header().Set("Cache-Control", "max-age=0, must-revalidate, no-cache")
		}
	}

	m.Use(httpstatic.Static(
		path.Join(rootDir, dir),
		httpstatic.StaticOptions{
			SkipLogging: true,
			Prefix:      prefix,
			AddHeaders:  headers,
		},
	))
}

func (hs *HTTPServer) metricsEndpointBasicAuthEnabled() bool {
	return hs.Cfg.MetricsEndpointBasicAuthUsername != "" && hs.Cfg.MetricsEndpointBasicAuthPassword != ""
}
