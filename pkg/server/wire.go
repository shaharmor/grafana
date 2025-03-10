//go:build wireinject
// +build wireinject

package server

import (
	"github.com/google/wire"
	sdkhttpclient "github.com/grafana/grafana-plugin-sdk-go/backend/httpclient"
	"github.com/grafana/grafana/pkg/api"
	"github.com/grafana/grafana/pkg/api/routing"
	"github.com/grafana/grafana/pkg/bus"
	"github.com/grafana/grafana/pkg/expr"
	"github.com/grafana/grafana/pkg/infra/httpclient"
	"github.com/grafana/grafana/pkg/infra/httpclient/httpclientprovider"
	"github.com/grafana/grafana/pkg/infra/kvstore"
	"github.com/grafana/grafana/pkg/infra/localcache"
	"github.com/grafana/grafana/pkg/infra/metrics"
	"github.com/grafana/grafana/pkg/infra/remotecache"
	"github.com/grafana/grafana/pkg/infra/serverlock"
	"github.com/grafana/grafana/pkg/infra/tracing"
	"github.com/grafana/grafana/pkg/infra/usagestats"
	uss "github.com/grafana/grafana/pkg/infra/usagestats/service"
	"github.com/grafana/grafana/pkg/login/social"
	"github.com/grafana/grafana/pkg/models"
	"github.com/grafana/grafana/pkg/plugins"
	"github.com/grafana/grafana/pkg/plugins/backendplugin/coreplugin"
	"github.com/grafana/grafana/pkg/plugins/manager"
	"github.com/grafana/grafana/pkg/plugins/manager/loader"
	"github.com/grafana/grafana/pkg/plugins/plugincontext"
	"github.com/grafana/grafana/pkg/services/accesscontrol/resourceservices"
	"github.com/grafana/grafana/pkg/services/alerting"
	"github.com/grafana/grafana/pkg/services/auth/jwt"
	"github.com/grafana/grafana/pkg/services/cleanup"
	"github.com/grafana/grafana/pkg/services/contexthandler"
	"github.com/grafana/grafana/pkg/services/dashboardimport"
	dashboardimportservice "github.com/grafana/grafana/pkg/services/dashboardimport/service"
	"github.com/grafana/grafana/pkg/services/dashboardsnapshots"
	"github.com/grafana/grafana/pkg/services/datasourceproxy"
	"github.com/grafana/grafana/pkg/services/datasources"
	"github.com/grafana/grafana/pkg/services/featuremgmt"
	"github.com/grafana/grafana/pkg/services/hooks"
	"github.com/grafana/grafana/pkg/services/libraryelements"
	"github.com/grafana/grafana/pkg/services/librarypanels"
	"github.com/grafana/grafana/pkg/services/live"
	"github.com/grafana/grafana/pkg/services/live/pushhttp"
	"github.com/grafana/grafana/pkg/services/login"
	"github.com/grafana/grafana/pkg/services/login/authinfoservice"
	"github.com/grafana/grafana/pkg/services/login/loginservice"
	"github.com/grafana/grafana/pkg/services/ngalert"
	ngmetrics "github.com/grafana/grafana/pkg/services/ngalert/metrics"
	"github.com/grafana/grafana/pkg/services/notifications"
	"github.com/grafana/grafana/pkg/services/oauthtoken"
	"github.com/grafana/grafana/pkg/services/plugindashboards"
	"github.com/grafana/grafana/pkg/services/pluginsettings"
	"github.com/grafana/grafana/pkg/services/query"
	"github.com/grafana/grafana/pkg/services/queryhistory"
	"github.com/grafana/grafana/pkg/services/quota"
	"github.com/grafana/grafana/pkg/services/rendering"
	"github.com/grafana/grafana/pkg/services/schemaloader"
	"github.com/grafana/grafana/pkg/services/search"
	"github.com/grafana/grafana/pkg/services/secrets"
	secretsDatabase "github.com/grafana/grafana/pkg/services/secrets/database"
	secretsManager "github.com/grafana/grafana/pkg/services/secrets/manager"
	"github.com/grafana/grafana/pkg/services/serviceaccounts"
	serviceaccountsmanager "github.com/grafana/grafana/pkg/services/serviceaccounts/manager"
	"github.com/grafana/grafana/pkg/services/shorturls"
	"github.com/grafana/grafana/pkg/services/sqlstore"
	"github.com/grafana/grafana/pkg/services/teamguardian"
	teamguardianDatabase "github.com/grafana/grafana/pkg/services/teamguardian/database"
	teamguardianManager "github.com/grafana/grafana/pkg/services/teamguardian/manager"
	"github.com/grafana/grafana/pkg/services/thumbs"
	"github.com/grafana/grafana/pkg/services/updatechecker"
	"github.com/grafana/grafana/pkg/setting"
	"github.com/grafana/grafana/pkg/tsdb/azuremonitor"
	"github.com/grafana/grafana/pkg/tsdb/cloudmonitoring"
	"github.com/grafana/grafana/pkg/tsdb/cloudwatch"
	"github.com/grafana/grafana/pkg/tsdb/elasticsearch"
	"github.com/grafana/grafana/pkg/tsdb/grafanads"
	"github.com/grafana/grafana/pkg/tsdb/graphite"
	"github.com/grafana/grafana/pkg/tsdb/influxdb"
	"github.com/grafana/grafana/pkg/tsdb/legacydata"
	legacydataservice "github.com/grafana/grafana/pkg/tsdb/legacydata/service"
	"github.com/grafana/grafana/pkg/tsdb/loki"
	"github.com/grafana/grafana/pkg/tsdb/mssql"
	"github.com/grafana/grafana/pkg/tsdb/mysql"
	"github.com/grafana/grafana/pkg/tsdb/opentsdb"
	"github.com/grafana/grafana/pkg/tsdb/postgres"
	"github.com/grafana/grafana/pkg/tsdb/prometheus"
	"github.com/grafana/grafana/pkg/tsdb/tempo"
	"github.com/grafana/grafana/pkg/tsdb/testdatasource"
)

var wireBasicSet = wire.NewSet(
	legacydataservice.ProvideService,
	wire.Bind(new(legacydata.RequestHandler), new(*legacydataservice.Service)),
	alerting.ProvideAlertEngine,
	wire.Bind(new(alerting.UsageStatsQuerier), new(*alerting.AlertEngine)),
	setting.NewCfgFromArgs,
	New,
	api.ProvideHTTPServer,
	query.ProvideService,
	bus.ProvideBus,
	wire.Bind(new(bus.Bus), new(*bus.InProcBus)),
	thumbs.ProvideService,
	rendering.ProvideService,
	wire.Bind(new(rendering.Service), new(*rendering.RenderingService)),
	routing.ProvideRegister,
	wire.Bind(new(routing.RouteRegister), new(*routing.RouteRegisterImpl)),
	hooks.ProvideService,
	kvstore.ProvideService,
	localcache.ProvideService,
	updatechecker.ProvideService,
	uss.ProvideService,
	wire.Bind(new(usagestats.Service), new(*uss.UsageStats)),
	manager.ProvideService,
	wire.Bind(new(plugins.Client), new(*manager.PluginManager)),
	wire.Bind(new(plugins.Store), new(*manager.PluginManager)),
	wire.Bind(new(plugins.StaticRouteResolver), new(*manager.PluginManager)),
	wire.Bind(new(plugins.PluginDashboardManager), new(*manager.PluginManager)),
	wire.Bind(new(plugins.RendererManager), new(*manager.PluginManager)),
	coreplugin.ProvideCoreRegistry,
	loader.ProvideService,
	wire.Bind(new(plugins.Loader), new(*loader.Loader)),
	wire.Bind(new(plugins.ErrorResolver), new(*loader.Loader)),
	cloudwatch.ProvideService,
	cloudmonitoring.ProvideService,
	azuremonitor.ProvideService,
	postgres.ProvideService,
	mysql.ProvideService,
	mssql.ProvideService,
	httpclientprovider.New,
	wire.Bind(new(httpclient.Provider), new(*sdkhttpclient.Provider)),
	serverlock.ProvideService,
	cleanup.ProvideService,
	shorturls.ProvideService,
	wire.Bind(new(shorturls.Service), new(*shorturls.ShortURLService)),
	queryhistory.ProvideService,
	wire.Bind(new(queryhistory.Service), new(*queryhistory.QueryHistoryService)),
	quota.ProvideService,
	remotecache.ProvideService,
	loginservice.ProvideService,
	wire.Bind(new(login.Service), new(*loginservice.Implementation)),
	authinfoservice.ProvideAuthInfoService,
	wire.Bind(new(login.AuthInfoService), new(*authinfoservice.Implementation)),
	datasourceproxy.ProvideService,
	search.ProvideService,
	live.ProvideService,
	pushhttp.ProvideService,
	plugincontext.ProvideService,
	contexthandler.ProvideService,
	jwt.ProvideService,
	wire.Bind(new(models.JWTService), new(*jwt.AuthService)),
	schemaloader.ProvideService,
	ngalert.ProvideService,
	librarypanels.ProvideService,
	wire.Bind(new(librarypanels.Service), new(*librarypanels.LibraryPanelService)),
	libraryelements.ProvideService,
	wire.Bind(new(libraryelements.Service), new(*libraryelements.LibraryElementService)),
	notifications.ProvideService,
	notifications.ProvideSmtpService,
	tracing.ProvideService,
	metrics.ProvideService,
	testdatasource.ProvideService,
	opentsdb.ProvideService,
	social.ProvideService,
	influxdb.ProvideService,
	wire.Bind(new(social.Service), new(*social.SocialService)),
	oauthtoken.ProvideService,
	wire.Bind(new(oauthtoken.OAuthTokenService), new(*oauthtoken.Service)),
	tempo.ProvideService,
	loki.ProvideService,
	graphite.ProvideService,
	prometheus.ProvideService,
	elasticsearch.ProvideService,
	secretsManager.ProvideSecretsService,
	wire.Bind(new(secrets.Service), new(*secretsManager.SecretsService)),
	secretsDatabase.ProvideSecretsStore,
	wire.Bind(new(secrets.Store), new(*secretsDatabase.SecretsStoreImpl)),
	grafanads.ProvideService,
	dashboardsnapshots.ProvideService,
	datasources.ProvideService,
	pluginsettings.ProvideService,
	alerting.ProvideService,
	serviceaccountsmanager.ProvideServiceAccountsService,
	wire.Bind(new(serviceaccounts.Service), new(*serviceaccountsmanager.ServiceAccountsService)),
	expr.ProvideService,
	teamguardianDatabase.ProvideTeamGuardianStore,
	wire.Bind(new(teamguardian.Store), new(*teamguardianDatabase.TeamGuardianStoreImpl)),
	teamguardianManager.ProvideService,
	wire.Bind(new(teamguardian.TeamGuardian), new(*teamguardianManager.Service)),
	wire.Bind(new(authinfoservice.Service), new(*authinfoservice.Implementation)),
	featuremgmt.ProvideManagerService,
	featuremgmt.ProvideToggles,
	resourceservices.ProvideResourceServices,
	dashboardimportservice.ProvideService,
	wire.Bind(new(dashboardimport.Service), new(*dashboardimportservice.ImportDashboardService)),
	plugindashboards.ProvideService,
)

var wireSet = wire.NewSet(
	wireBasicSet,
	sqlstore.ProvideService,
	ngmetrics.ProvideService,
	wire.Bind(new(notifications.Service), new(*notifications.NotificationService)),
	wire.Bind(new(notifications.WebhookSender), new(*notifications.NotificationService)),
	wire.Bind(new(notifications.EmailSender), new(*notifications.NotificationService)),
)

var wireTestSet = wire.NewSet(
	wireBasicSet,
	ProvideTestEnv,
	sqlstore.ProvideServiceForTests,
	ngmetrics.ProvideServiceForTest,

	notifications.MockNotificationService,
	wire.Bind(new(notifications.Service), new(*notifications.NotificationServiceMock)),
	wire.Bind(new(notifications.WebhookSender), new(*notifications.NotificationServiceMock)),
	wire.Bind(new(notifications.EmailSender), new(*notifications.NotificationServiceMock)),
)

func Initialize(cla setting.CommandLineArgs, opts Options, apiOpts api.ServerOptions) (*Server, error) {
	wire.Build(wireExtsSet)
	return &Server{}, nil
}

func InitializeForTest(cla setting.CommandLineArgs, opts Options, apiOpts api.ServerOptions) (*TestEnv, error) {
	wire.Build(wireExtsTestSet)
	return &TestEnv{Server: &Server{}, SQLStore: &sqlstore.SQLStore{}}, nil
}
