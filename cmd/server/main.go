// appkit-demo 服务入口：装配已安装的 lynx-mods 模块并托管生命周期。
// 本文件由 lynx-mods init 生成，后续基本不再变——模块增减只改 modules/modules.go。
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/lynxland/lynx"
	"github.com/lynxland/lynx/contrib/schedule"
	"github.com/lynxland/lynx/contrib/zap"

	"github.com/lynxland/appkit"
	"github.com/lynxland/appkit/chain"
	"github.com/lynxland/appkit/compose"
	"github.com/lynxland/appkit/contrib/bunx"
	"github.com/lynxland/appkit/idem"
	"github.com/lynxland/appkit/periodic"
	"github.com/lynxland/appkit/ratelimit"
	grpcapiauthn "github.com/lynxland/grpcapi/authn"
	grpcapiopenapi "github.com/lynxland/grpcapi/openapi"
	grpcapipolicy "github.com/lynxland/grpcapi/policy"
	grpcapiprotopolicy "github.com/lynxland/grpcapi/policy/protopolicy"
	"github.com/lynxland/grpcapi/principal"
	grpcapiserver "github.com/lynxland/grpcapi/server"
	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/lynxland/appkit-demo/internal/bootstrap"
	"github.com/lynxland/appkit-demo/internal/config"
	"github.com/lynxland/appkit-demo/modules"
)

func main() {
	runner := lynx.NewRunner(setup,
		lynx.WithName("appkit-demo"),
		lynx.WithShutdownTimeout(30*time.Second),
		lynx.WithBindConfigFunc(config.BindConfig),
		lynx.WithLoggerProvider(func(app lynx.AppContext) (*slog.Logger, error) {
			return zap.NewLogger(app)
		}),
	)
	runner.Run()
}

func setup(app lynx.App) error {
	logger := app.Logger()

	cfg, err := config.Load(app.Config())
	if err != nil {
		return err
	}
	deps, cleanup, err := bootstrap.NewDeps(app, cfg, logger)
	if err != nil {
		return err
	}
	app.OnPostStop(cleanup)

	installed := modules.Installed()
	agg, err := compose.AssembleAll(deps, installed)
	if err != nil {
		return err
	}

	// 策略集：全部已装模块的 proto 注解（fail-closed 启动断言由 grpcapi 承担）。
	set, err := buildPolicies(app.Context(), installed)
	if err != nil {
		return err
	}

	// 模块贡献的认证后增强器（access 的 roles/permissions claims 等）。
	authnOpts := grpcapiauthn.Options{Enrichers: agg.Enrichers}

	// 应用级幂等执法（dogfood 首循环⑤采纳）：创建型动词挂 Idempotency-Key
	// 语义——同键同载荷回放、异载荷拒复用、失败可重试（idem v0.5.5）。
	// Stage 为应用所有（非模块贡献），注入 agg 后由 DefaultChain 统一入链
	//（Order 45：authn 后 authz 前）。
	enforced := map[string]bool{
		"/organization.v1.OrganizationService/CreateOrganization": true,
	}
	// 应用级限流（dogfood 首循环⑥采纳）：组织创建 3 次/分钟/用户
	//（滑动窗口——固定窗口边界突刺不复现）；429 携带 RetryInfo，
	// gateway 转 Retry-After 头。
	rateLimiter := ratelimit.NewSlidingWindow(3, time.Minute)
	agg.Stages = append(agg.Stages,
		idem.New(idem.NewMemStore(), idem.Options{
			Enforced: func(fullMethod string) bool { return enforced[fullMethod] },
		}),
		ratelimit.Stage(ratelimit.Options{
			Limiter: rateLimiter,
			Key: func(ctx context.Context, fullMethod string) string {
				if !enforced[fullMethod] {
					return "" // 未登记方法不限流
				}
				return "org-create:" + rateScope(ctx)
			},
		}),
	)

	// 模块贡献拦截器 Stage（audit=50 等）时接管链构建——复刻 server 默认链
	// 的知识在 chain.DefaultChain（I-08：修复经 appkit semver 推送，
	// scaffold 生成物不再持有这段易错逻辑）。
	chain, err := chain.DefaultChain(logger, set, agg, authnOpts)
	if err != nil {
		return err
	}

	srv, err := grpcapiserver.New(app.Context(), grpcapiserver.Config{
		Policy:               set,
		Services:             agg.Registrations,
		ExtraRoutes:          agg.ExtraRoutes,
		Authenticators:       agg.Authenticators,
		AuthnOptions:         authnOpts,
		Chain:                chain,
		GRPCAddr:             cfg.Server.GRPCAddr,
		HTTPAddr:             cfg.Server.HTTPAddr,
		AllowNoAuthenticator: len(agg.Authenticators) == 0,
		OpenAPI:              &grpcapiopenapi.Info{Title: "appkit-demo", Version: "v1"},
		Logger:               logger,
	})
	if err != nil {
		return err
	}
	// 应用级周期作业（dogfood 首循环⑧采纳）：审计保留清扫——超窗行
	// 删除（幂等，预算内自然收束；优雅关停不腰斩在途轮次）。
	app.Register(periodic.New("audit-retention", time.Hour, 5*time.Minute, func(ctx context.Context) error {
		return auditSweep(ctx, cfg)
	}, logger).RunAtStart())

	app.Register(bootstrap.NewAPIService(srv, logger))

	printRouteTable(installed, set)

	if len(agg.Tasks) > 0 {
		scheduler, err := schedule.NewScheduler(agg.Tasks)
		if err != nil {
			return err
		}
		app.Register(scheduler)
	}
	app.Register(agg.Services...)
	return nil
}

func buildPolicies(ctx context.Context, installed []appkit.InstalledModule) (*grpcapipolicy.Set, error) {
	files := compose.PolicyFiles(compose.Descriptors(installed))
	if len(files) == 0 {
		return grpcapipolicy.NewSet()
	}
	return grpcapipolicy.Build(ctx, grpcapipolicy.FromFiles(files...), grpcapipolicy.Options{
		Provider: grpcapiprotopolicy.New(grpcapiprotopolicy.Options{}),
		Registry: grpcapipolicy.DefaultRegistry(),
	})
}

// printRouteTable 启动期打印 HTTP 路由表（proto 的 google.api.http 注解驱动），
// 免去翻 proto 找路径：方法、路径、RPC 全名与认证要求一屏可见。手写的
// ExtraRoutes 与 gRPC 面（:9060）不在此列。
func printRouteTable(installed []appkit.InstalledModule, set *grpcapipolicy.Set) {
	type routeRow struct{ method, path, rpc, auth string }
	var rows []routeRow
	for _, fd := range compose.PolicyFiles(compose.Descriptors(installed)) {
		services := fd.Services()
		for i := 0; i < services.Len(); i++ {
			sd := services.Get(i)
			methods := sd.Methods()
			for j := 0; j < methods.Len(); j++ {
				md := methods.Get(j)
				rpcName := string(sd.FullName()) + "/" + string(md.Name())
				auth := "auth"
				if set.IsAnonymous("/" + rpcName) {
					auth = "anonymous"
				}
				for _, hr := range httpRules(md) {
					rows = append(rows, routeRow{method: hr[0], path: hr[1], rpc: rpcName, auth: auth})
				}
			}
		}
	}
	if len(rows) == 0 {
		return
	}
	sort.Slice(rows, func(a, b int) bool { return rows[a].path < rows[b].path })
	w := tabwriter.NewWriter(os.Stdout, 2, 0, 2, ' ', 0)
	fmt.Fprintln(w, "HTTP routes:")
	for _, r := range rows {
		fmt.Fprintf(w, "  %s\t%s\t%s\t%s\n", r.method, r.path, r.rpc, r.auth)
	}
	_ = w.Flush()
}

// httpRules 抽取方法的 google.api.http 注解（primary 与 additional_bindings）。
func httpRules(md protoreflect.MethodDescriptor) [][2]string {
	if md.Options() == nil {
		return nil
	}
	rule, ok := proto.GetExtension(md.Options(), annotations.E_Http).(*annotations.HttpRule)
	if !ok || rule == nil {
		return nil
	}
	add := func(r *annotations.HttpRule, out *[][2]string) {
		switch p := r.Pattern.(type) {
		case *annotations.HttpRule_Get:
			*out = append(*out, [2]string{"GET", p.Get})
		case *annotations.HttpRule_Put:
			*out = append(*out, [2]string{"PUT", p.Put})
		case *annotations.HttpRule_Post:
			*out = append(*out, [2]string{"POST", p.Post})
		case *annotations.HttpRule_Delete:
			*out = append(*out, [2]string{"DELETE", p.Delete})
		case *annotations.HttpRule_Patch:
			*out = append(*out, [2]string{"PATCH", p.Patch})
		case *annotations.HttpRule_Custom:
			*out = append(*out, [2]string{strings.ToUpper(p.Custom.GetKind()), p.Custom.GetPath()})
		}
	}
	var out [][2]string
	add(rule, &out)
	for _, ab := range rule.GetAdditionalBindings() {
		add(ab, &out)
	}
	return out
}

// rateScope 取限流键的调用方段（principal ID，匿名回退 anon）。
func rateScope(ctx context.Context) string {
	if p, ok := principal.From(ctx); ok && p.ID != "" {
		return p.ID
	}
	return "anon"
}

// auditSweep 删除审计表超窗行（保留 30 天；直接 SQL——审计保留是应用级
// 运维决策，不属任何单一模块）。幂等：按 occurred_at < cutoff 删除。
func auditSweep(ctx context.Context, cfg *config.Config) error {
	db, err := bunx.OpenDB(cfg.Data.Database.Source, bunx.Options{})
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	cutoff := time.Now().AddDate(0, 0, -30).UTC()
	res, err := db.NewDelete().TableExpr("audit_records").Where("occurred_at < ?", cutoff).Exec(ctx)
	if err != nil {
		return fmt.Errorf("audit sweep: %w", err)
	}
	if n, _ := res.RowsAffected(); n > 0 {
		slog.Default().Info("audit retention swept", "rows", n)
	}
	return nil
}
