// Package identity 是身份模块的 API 层：gRPC 处理器、Bearer 认证器与
// 装配入口（Module/Assemble）。
package identity

import (
	"context"
	"errors"

	identityv1 "github.com/lynxland/appkit-demo/genproto/identity/v1"
	"github.com/lynxland/appkit-demo/modules/identity/domain"
	appidentity "github.com/lynxland/appkit-demo/modules/identity/internal/app"

	"github.com/lynxland/grpcapi/principal"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/lynxland/appkit/errcode"
)

// Service 实现 IdentityService（薄转发：业务在用例层）。
type Service struct {
	identityv1.UnimplementedIdentityServiceServer
	uc *appidentity.UseCase
}

// NewService 构造处理器。
func NewService(uc *appidentity.UseCase) *Service { return &Service{uc: uc} }

// Register 处理注册（注册即登录）。
func (s *Service) Register(ctx context.Context, req *identityv1.RegisterRequest) (*identityv1.RegisterResponse, error) {
	user, token, err := s.uc.Register(ctx, req.GetEmail(), req.GetPassword(), req.GetDisplayName())
	if err != nil {
		return nil, toStatus(err)
	}
	return &identityv1.RegisterResponse{
		User:        toAPI(user),
		AccessToken: token.Value,
		TokenType:   "Bearer",
		ExpiresAt:   timestamppb.New(token.ExpiresAt),
	}, nil
}

// Login 处理登录。
func (s *Service) Login(ctx context.Context, req *identityv1.LoginRequest) (*identityv1.LoginResponse, error) {
	user, token, err := s.uc.Login(ctx, req.GetEmail(), req.GetPassword())
	if err != nil {
		return nil, toStatus(err)
	}
	return &identityv1.LoginResponse{
		User:        toAPI(user),
		AccessToken: token.Value,
		TokenType:   "Bearer",
		ExpiresAt:   timestamppb.New(token.ExpiresAt),
	}, nil
}

// GetMe 处理当前用户档案（principal 由 authn 拦截器注入）。
func (s *Service) GetMe(ctx context.Context, _ *identityv1.GetMeRequest) (*identityv1.GetMeResponse, error) {
	userID, err := requirePrincipal(ctx)
	if err != nil {
		return nil, err
	}
	user, err := s.uc.GetMe(ctx, userID)
	if err != nil {
		return nil, toStatus(err)
	}
	return &identityv1.GetMeResponse{User: toAPI(user)}, nil
}

// ChangePassword 处理自助改密。
func (s *Service) ChangePassword(ctx context.Context, req *identityv1.ChangePasswordRequest) (*emptypb.Empty, error) {
	userID, err := requirePrincipal(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.uc.ChangePassword(ctx, userID, req.GetOldPassword(), req.GetNewPassword()); err != nil {
		return nil, toStatus(err)
	}
	return &emptypb.Empty{}, nil
}

// requirePrincipal 取认证主体 ID。策略层已要求认证；这是 handler 侧的
// 双保险（fail-closed：principal 缺失不静默降级）。
func requirePrincipal(ctx context.Context) (string, error) {
	p, ok := principal.From(ctx)
	if !ok || p.ID == "" {
		return "", status.Error(codes.Unauthenticated, "authentication required")
	}
	return p.ID, nil
}

// toStatus 把区段哨兵错误映射为携带 ErrorInfo 的 grpc status
// （契约⑤：Domain=identity；非区段错误原样透传）。
func toStatus(err error) error {
	if err == nil {
		return nil
	}
	var info errcode.Info
	if !errors.As(err, &info) {
		return err
	}
	var code codes.Code
	switch info {
	case identity.ErrEmailTaken:
		code = codes.AlreadyExists
	case identity.ErrInvalidCredentials,
		identity.ErrTokenInvalid,
		identity.ErrTokenExpired:
		code = codes.Unauthenticated
	case identity.ErrUserNotFound:
		code = codes.NotFound
	case identity.ErrUserDisabled:
		code = codes.PermissionDenied
	case identity.ErrInvalidEmail,
		identity.ErrWeakPassword:
		code = codes.InvalidArgument
	default:
		code = codes.Internal
	}
	st := status.New(code, info.Error())
	withDetails, derr := st.WithDetails(&errdetails.ErrorInfo{
		Domain: info.Domain,
		Reason: info.Reason,
	})
	if derr != nil {
		return st.Err()
	}
	return withDetails.Err()
}

func toAPI(u *identity.User) *identityv1.User {
	return &identityv1.User{
		Id:          u.ID,
		Email:       u.Email,
		DisplayName: u.DisplayName,
		Status:      u.Status,
		CreatedAt:   timestamppb.New(u.CreatedAt),
		UpdatedAt:   timestamppb.New(u.UpdatedAt),
	}
}
