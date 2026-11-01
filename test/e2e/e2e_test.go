// Package e2e 是 demo 的进程内全链测试（dogfood 首循环⑦采纳，
// testkit v0.2.0）：七模块在隔离测试库上装配，经 bufconn gRPC 与
// httptest REST 走真实链路——register → login → Bearer me → 建组织 →
// 组织挂靠任务 → 改密事件联动吊销。取代 docker+curl 冒烟的不可回归
// 形态；PG 门控（LYNXSTART_TEST_PG_DSN）。
package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	identityv1 "github.com/lynx-go/lynx-start-demo/genproto/identity/v1"
	orgv1 "github.com/lynx-go/lynx-start-demo/genproto/organization/v1"
	sessionv1 "github.com/lynx-go/lynx-start-demo/genproto/session/v1"
	taskv1 "github.com/lynx-go/lynx-start-demo/genproto/task/v1"
	"github.com/lynx-go/lynx-start-demo/modules"
	"github.com/lynx-go/lynx-start/contrib/bunx/testkit"
	"google.golang.org/grpc/metadata"
)

func newHarness(t *testing.T) *testkit.Harness {
	t.Helper()
	return testkit.New(t, testkit.Options{
		Installed: modules.Installed(),
		Config: map[string]any{
			"identity.secret": "e2e-identity-secret-0123456789abcdef",
			"session.secret":  "e2e-session-secret-0123456789abcdef0",
		},
		Gateway: true,
	})
}

func registerLogin(t *testing.T, h *testkit.Harness, email, pass string) (token string, userID string) {
	t.Helper()
	ctx := context.Background()
	ids := identityv1.NewIdentityServiceClient(h.Conn)
	reg, err := ids.Register(ctx, &identityv1.RegisterRequest{
		Email: email, Password: pass, DisplayName: strings.Split(email, "@")[0],
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	login, err := ids.Login(ctx, &identityv1.LoginRequest{Email: email, Password: pass})
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	return login.AccessToken, reg.User.Id
}

func authCtx(ctx context.Context, token string) context.Context {
	return metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+token)
}

func TestFullChainRegisterLoginOrgTask(t *testing.T) {
	h := newHarness(t)
	token, _ := registerLogin(t, h, "e2e@demo.dev", "e2e-pass-12345")

	ids := identityv1.NewIdentityServiceClient(h.Conn)
	me, err := ids.GetMe(authCtx(context.Background(), token), &identityv1.GetMeRequest{})
	if err != nil {
		t.Fatalf("get-me: %v", err)
	}
	if me.User.Email != "e2e@demo.dev" {
		t.Fatalf("me.email = %s", me.User.Email)
	}

	// 建组织 → 组织挂靠任务（ADR-0008 姿势链路）。
	ctx := authCtx(context.Background(), token)
	orgs := orgv1.NewOrganizationServiceClient(h.Conn)
	org, err := orgs.CreateOrganization(ctx, &orgv1.CreateOrganizationRequest{Name: "E2E Org", Slug: "e2e-org"})
	if err != nil {
		t.Fatalf("create-org: %v", err)
	}
	tasks := taskv1.NewTaskServiceClient(h.Conn)
	task, err := tasks.CreateTask(ctx, &taskv1.CreateTaskRequest{OrganizationId: org.Id, Title: "e2e task"})
	if err != nil {
		t.Fatalf("create-task: %v", err)
	}
	if task.OrganizationId != org.Id || task.CreatorId != me.User.Id {
		t.Fatalf("task attribution: %+v", task)
	}
}

// TestRESTGatewayRegisterLogin REST 面（httptest）走同一后端。
func TestRESTGatewayRegisterLogin(t *testing.T) {
	h := newHarness(t)
	post := func(path string, body any, token string) (int, map[string]any) {
		raw, _ := json.Marshal(body)
		req, _ := http.NewRequest(http.MethodPost, h.HTTPURL+path, bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("rest %s: %v", path, err)
		}
		defer resp.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return resp.StatusCode, out
	}
	code, _ := post("/v1/identity:register", map[string]string{
		"email": "rest@demo.dev", "password": "rest-pass-1234", "displayName": "Rest",
	}, "")
	if code != http.StatusOK {
		t.Fatalf("rest register = %d", code)
	}
	code, out := post("/v1/identity:login", map[string]string{
		"email": "rest@demo.dev", "password": "rest-pass-1234",
	}, "")
	if code != http.StatusOK || out["access_token"] == nil {
		t.Fatalf("rest login = %d %v", code, out)
	}
}

// TestPasswordChangedRevokesSessions 改密事件联动：session 会话被全局
// 吊销（Refresh 被拒）。identity access token 是无吊销 JWT（TTL 内有效，
// 模块既定取舍）——联动面在 session 的 refresh 侧，异步投递经 Eventually
// 确定性等待。
func TestPasswordChangedRevokesSessions(t *testing.T) {
	h := newHarness(t)
	token, _ := registerLogin(t, h, "revoke@demo.dev", "revoke-pass-123")

	ctx := authCtx(context.Background(), token)
	sessions := sessionv1.NewSessionServiceClient(h.Conn)
	created, err := sessions.CreateSession(ctx, &sessionv1.CreateSessionRequest{})
	if err != nil {
		t.Fatalf("create-session: %v", err)
	}

	// 改密 → identity.user.password-changed → watcher 吊销全部会话。
	ids := identityv1.NewIdentityServiceClient(h.Conn)
	if _, err := ids.ChangePassword(ctx, &identityv1.ChangePasswordRequest{
		OldPassword: "revoke-pass-123", NewPassword: "revoke-pass-456",
	}); err != nil {
		t.Fatalf("change-password: %v", err)
	}

	// 异步联动：旧 refresh token 最终被拒。
	testkit.Eventually(t, 5e9, func() error {
		if _, err := sessions.Refresh(context.Background(), &sessionv1.RefreshRequest{RefreshToken: created.RefreshToken}); err == nil {
			return fmt.Errorf("revoked session still refreshable")
		}
		return nil
	})
	// 事件捕获面同证：改密事件确实发布过。
	found := false
	for _, name := range h.Events.Names() {
		if name == "identity.user.password-changed" {
			found = true
		}
	}
	if !found {
		t.Error("password-changed event not captured")
	}
}
