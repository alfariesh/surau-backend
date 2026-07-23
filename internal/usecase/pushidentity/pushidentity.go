package pushidentity

import (
	"context"
	"crypto/ecdsa"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/alfariesh/surau-backend/internal/entity"
	jwtlib "github.com/golang-jwt/jwt/v5"
)

const (
	IdentitySchemaV1 = "surau.push.identity.v1"
	eligibleIntent   = "notify_khatam_milestones"
	routeIntent      = "open_khatam_progress"
	maxTTL           = time.Hour
	minSecretBytes   = 32
)

var (
	ErrInactiveSession = errors.New("inactive auth session")
	errInvalidConfig   = errors.New("invalid push identity configuration")
)

type Options struct {
	AppID          string
	PrivateKeyFile string
	BindingSecret  string
	TTL            time.Duration
	Now            func() time.Time
}

type Users interface {
	ListSessions(ctx context.Context, userID string) ([]entity.AuthSession, error)
	GetUser(ctx context.Context, userID string) (entity.User, error)
}

type UseCase struct {
	users         Users
	appID         string
	privateKey    *ecdsa.PrivateKey
	bindingSecret []byte
	ttl           time.Duration
	now           func() time.Time
}

type oneSignalClaims struct {
	Identity struct {
		ExternalID string `json:"external_id"`
	} `json:"identity"`
	jwtlib.RegisteredClaims
}

func New(users Users, opts Options) (*UseCase, error) {
	if users == nil || strings.TrimSpace(opts.AppID) == "" {
		return nil, fmt.Errorf("%w: users and app id are required", errInvalidConfig)
	}

	if opts.TTL <= 0 || opts.TTL > maxTTL {
		return nil, fmt.Errorf("%w: TTL must be positive and at most one hour", errInvalidConfig)
	}

	if len(opts.BindingSecret) < minSecretBytes {
		return nil, fmt.Errorf("%w: owner binding secret must be at least %d bytes", errInvalidConfig, minSecretBytes)
	}

	pemBytes, err := os.ReadFile(strings.TrimSpace(opts.PrivateKeyFile))
	if err != nil {
		return nil, fmt.Errorf("read OneSignal identity private key: %w", err)
	}

	key, err := jwtlib.ParseECPrivateKeyFromPEM(pemBytes)
	if err != nil {
		return nil, fmt.Errorf("parse OneSignal ES256 private key: %w", err)
	}

	if key.Curve.Params().Name != "P-256" {
		return nil, fmt.Errorf("%w: private key must use P-256", errInvalidConfig)
	}

	if opts.Now == nil {
		opts.Now = time.Now
	}

	return &UseCase{
		users: users, appID: strings.TrimSpace(opts.AppID), privateKey: key,
		bindingSecret: []byte(opts.BindingSecret), ttl: opts.TTL, now: opts.Now,
	}, nil
}

func (uc *UseCase) Issue(ctx context.Context, userID, familyID string) (entity.PushIdentityToken, error) {
	user, err := uc.activeUser(ctx, userID, familyID)
	if err != nil {
		return entity.PushIdentityToken{}, err
	}

	now := uc.now().UTC()
	expiresAt := now.Add(uc.ttl)
	claims := oneSignalClaims{RegisteredClaims: jwtlib.RegisteredClaims{
		Issuer: uc.appID, IssuedAt: jwtlib.NewNumericDate(now), ExpiresAt: jwtlib.NewNumericDate(expiresAt),
	}}
	claims.Identity.ExternalID = user.ID

	token, err := jwtlib.NewWithClaims(jwtlib.SigningMethodES256, claims).SignedString(uc.privateKey)
	if err != nil {
		return entity.PushIdentityToken{}, fmt.Errorf("sign OneSignal identity token: %w", err)
	}

	return entity.PushIdentityToken{
		SchemaVersion: IdentitySchemaV1, IdentityToken: token, ExternalID: user.ID,
		OwnerBinding: uc.binding(user.ID, user.TokenVersion), ExpiresAt: expiresAt,
		ExpiresIn: int64(uc.ttl.Seconds()), EligibleIntents: []string{eligibleIntent},
	}, nil
}

func (uc *UseCase) Resolve(ctx context.Context, userID, familyID string, in entity.PushRouteInput) entity.PushRouteResolution {
	user, err := uc.activeUser(ctx, userID, familyID)
	if err != nil || in.SchemaVersion != entity.PushDataSchemaV1 {
		return entity.PushRouteResolution{Destination: "home"}
	}

	if in.Scope == "public" {
		return entity.PushRouteResolution{Destination: "intent", Intent: in.Intent}
	}

	expected := uc.binding(user.ID, user.TokenVersion)
	if in.Scope != "personal" || in.Intent != routeIntent ||
		!hmac.Equal([]byte(expected), []byte(in.OwnerBinding)) {
		return entity.PushRouteResolution{Destination: "home"}
	}

	return entity.PushRouteResolution{Destination: "intent", Intent: routeIntent}
}

func (uc *UseCase) activeUser(ctx context.Context, userID, familyID string) (entity.User, error) {
	if strings.TrimSpace(userID) == "" || strings.TrimSpace(familyID) == "" {
		return entity.User{}, ErrInactiveSession
	}

	sessions, err := uc.users.ListSessions(ctx, userID)
	if err != nil {
		return entity.User{}, fmt.Errorf("list active sessions: %w", err)
	}

	active := false

	for i := range sessions {
		if sessions[i].FamilyID == familyID {
			active = true

			break
		}
	}

	if !active {
		return entity.User{}, ErrInactiveSession
	}

	user, err := uc.users.GetUser(ctx, userID)
	if err != nil {
		return entity.User{}, fmt.Errorf("get authenticated user: %w", err)
	}

	return user, nil
}

func (uc *UseCase) binding(userID string, tokenVersion int64) string {
	mac := hmac.New(sha256.New, uc.bindingSecret)
	_, _ = fmt.Fprintf(mac, "surau-owner-v1\x00%s\x00%d", userID, tokenVersion)

	return "ob1." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// OwnerBinding creates the opaque account-generation proof embedded in personal push data.
func (uc *UseCase) OwnerBinding(userID string, tokenVersion int64) string {
	return uc.binding(userID, tokenVersion)
}
