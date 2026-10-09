package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/policies"
	"github.com/macrowallets/waas/app/services/account"
)

var (
	// ErrUserLookup is a sign-in whose user lookup failed for a reason other
	// than a missing user: an outage, not a bad credential.
	ErrUserLookup = errors.New("look up the user")
	// ErrInvalidCredentials is an unknown email or a wrong password. Which one
	// is not told.
	ErrInvalidCredentials = errors.New("invalid credentials")
	// ErrUserInactive is a user whose status may not hold a session.
	ErrUserInactive = errors.New("user is not active")
	// ErrUserSuspended is a user a platform admin suspended.
	ErrUserSuspended = errors.New("user is suspended")
	// ErrSecondFactorMissing is a 2FA answer with neither a code nor a recovery code.
	ErrSecondFactorMissing = errors.New("code or recovery_code is required")
	// ErrRefreshInvalid is a refresh token that matches no live one, or whose
	// user is gone.
	ErrRefreshInvalid = errors.New("invalid or expired refresh token")
	// ErrUserNotCreated is a registration whose rows could not be written.
	ErrUserNotCreated = errors.New("create the user")
	// ErrAccountsUnavailable is a sign-in whose accounts could not be read.
	// No session is issued without them.
	ErrAccountsUnavailable = errors.New("load the accounts")
	// ErrSessionNotCreated is a sign-in that passed but whose session could
	// not be issued.
	ErrSessionNotCreated = errors.New("create the session")
)

// SignInUsers finds the user a sign-in is for (users.Service).
type SignInUsers interface {
	FindByEmail(ctx context.Context, email string) (*models.User, error)
	FindByID(ctx context.Context, id uuid.UUID) (*models.User, error)
}

// SignInAccounts is the account side of a sign-in (account.Service): the
// rows a registration writes and the accounts a sign-in answers with.
type SignInAccounts interface {
	Onboard(ctx context.Context, input account.OnboardInput, welcome account.WelcomeDispatch) (*models.User, error)
	SignInAccounts(ctx context.Context, userID uuid.UUID, defaultAccountID *uuid.UUID) (account.SignInAccounts, error)
}

// WelcomeMail sends the welcome mail of a new user (credentialmail.Service).
type WelcomeMail interface {
	SendWelcome(ctx context.Context, userID uuid.UUID) error
}

// RefreshTokenRotator finds the live refresh tokens and revokes the one a
// refresh spends (sessions.RefreshTokens).
type RefreshTokenRotator interface {
	FindValidTokens(ctx context.Context) ([]models.RefreshToken, error)
	RevokeIfActive(ctx context.Context, id uuid.UUID) (bool, error)
}

// SignIn signs dashboard users in and out: registration, the password login
// and its TOTP challenge, the refresh of a session and the logout.
type SignIn struct {
	users     SignInUsers
	accounts  SignInAccounts
	welcome   WelcomeMail
	passwords *Service
	twoFactor *TwoFactorLogin
	sessions  *SessionIssuer
	refresh   RefreshTokenRotator
	revoker   *SessionRevoker
}

// SignInDeps is everything SignIn uses. Every field is required.
type SignInDeps struct {
	Users     SignInUsers
	Accounts  SignInAccounts
	Welcome   WelcomeMail
	Passwords *Service
	TwoFactor *TwoFactorLogin
	Sessions  *SessionIssuer
	Refresh   RefreshTokenRotator
	Revoker   *SessionRevoker
}

// NewSignIn builds the sign-in flows from SignInDeps.
func NewSignIn(deps SignInDeps) (*SignIn, error) {
	if deps.Users == nil || deps.Accounts == nil || deps.Welcome == nil || deps.Passwords == nil ||
		deps.TwoFactor == nil || deps.Sessions == nil || deps.Refresh == nil || deps.Revoker == nil {
		return nil, errors.New("auth: sign in: all dependencies are required")
	}
	return &SignIn{
		users:     deps.Users,
		accounts:  deps.Accounts,
		welcome:   deps.Welcome,
		passwords: deps.Passwords,
		twoFactor: deps.TwoFactor,
		sessions:  deps.Sessions,
		refresh:   deps.Refresh,
		revoker:   deps.Revoker,
	}, nil
}

// RegisterInput is POST /v1/auth/register once its form request passed.
type RegisterInput struct {
	Email            string
	Password         string
	FullName         string
	OrganizationName string
}

// Registered is a new user, signed in with a session JWT and no refresh
// token, with the accounts the registration created.
type Registered struct {
	User        *models.User
	Accounts    account.SignInAccounts
	AccessToken string
}

// LoginInput is POST /v1/auth/login once its form request passed.
type LoginInput struct {
	Email    string
	Password string
}

// VerifyInput is POST /v1/auth/2fa/verify once its form request passed.
type VerifyInput struct {
	ChallengeToken string
	Code           string
	RecoveryCode   string
}

// SignedIn is a sign-in that passed: a session (User, Tokens, Accounts) or,
// for a password login of a user with 2FA, only the Challenge to complete.
type SignedIn struct {
	User      *models.User
	Tokens    SessionTokens
	Accounts  account.SignInAccounts
	Challenge *TwoFactorChallenge
}

// Register creates the user, both accounts and the memberships in one
// transaction, sends the welcome mail once they committed (a failure is
// logged; the user stands), and signs the user in with guard. Every failed
// step names itself.
func (s *SignIn) Register(ctx context.Context, guard SessionGuard, in RegisterInput) (Registered, error) {
	hash, err := s.passwords.HashPassword(in.Password)
	if err != nil {
		return Registered{}, fmt.Errorf("%w: %w", ErrPasswordNotHashed, err)
	}
	user, err := s.accounts.Onboard(ctx, account.OnboardInput{
		Email:            in.Email,
		PasswordHash:     hash,
		FullName:         in.FullName,
		OrganizationName: in.OrganizationName,
	}, func(userID uuid.UUID) error {
		if mailErr := s.welcome.SendWelcome(ctx, userID); mailErr != nil {
			slog.Error("auth: send welcome mail failed")
		}
		return nil
	})
	if err != nil {
		return Registered{}, fmt.Errorf("%w: %w", ErrUserNotCreated, err)
	}
	accounts, err := s.signInAccounts(ctx, user)
	if err != nil {
		return Registered{}, err
	}
	accessToken, err := guard.LoginUsingID(user.ID.String())
	if err != nil {
		return Registered{}, fmt.Errorf("%w: %w", ErrSessionNotCreated, err)
	}
	return Registered{User: user, Accounts: accounts, AccessToken: accessToken}, nil
}

// Login checks the email and password. A user with 2FA gets a challenge
// instead of a session; anyone else gets a session signed with guard and
// their accounts. An unknown email spends the bcrypt time a wrong password
// does, and both are ErrInvalidCredentials.
func (s *SignIn) Login(ctx context.Context, guard SessionGuard, in LoginInput) (SignedIn, error) {
	user, err := s.users.FindByEmail(ctx, in.Email)
	if err != nil && !errors.Is(err, models.ErrRepositoryNotFound) {
		return SignedIn{}, fmt.Errorf("%w: %w", ErrUserLookup, err)
	}
	if user == nil {
		s.passwords.CheckPassword(in.Password, DummyPasswordHash)
		return SignedIn{}, ErrInvalidCredentials
	}
	if !s.passwords.CheckPassword(in.Password, user.PasswordHash) {
		return SignedIn{}, ErrInvalidCredentials
	}
	if err := mayHoldSession(user); err != nil {
		return SignedIn{}, err
	}

	if user.TotpEnabled {
		if err := s.revoker.AwaitIssuable(user.SessionsRevokedAt); err != nil {
			return SignedIn{}, fmt.Errorf("%w: begin 2fa: %w", ErrSessionNotCreated, err)
		}
		challenge, err := s.twoFactor.Begin(user)
		if err != nil {
			return SignedIn{}, fmt.Errorf("%w: begin 2fa: %w", ErrSessionNotCreated, err)
		}
		return SignedIn{User: user, Challenge: &challenge}, nil
	}
	return s.session(ctx, guard, user)
}

// Verify completes a 2FA challenge with a code or a recovery code and signs
// the user in with guard. The challenge's refusals are returned as they are.
func (s *SignIn) Verify(ctx context.Context, guard SessionGuard, in VerifyInput) (SignedIn, error) {
	if in.Code == "" && in.RecoveryCode == "" {
		return SignedIn{}, ErrSecondFactorMissing
	}
	user, err := s.twoFactor.Complete(in.ChallengeToken, in.Code, in.RecoveryCode)
	if err != nil {
		return SignedIn{}, err
	}
	if err := mayHoldSession(user); err != nil {
		return SignedIn{}, err
	}
	return s.session(ctx, guard, user)
}

// Refresh spends a refresh token and issues a new session for its user,
// signed with guard. A token that matches no live one, that another request
// already rotated, or whose user is gone is ErrRefreshInvalid; a token list
// that could not be read is logged and is that too.
func (s *SignIn) Refresh(ctx context.Context, guard SessionGuard, rawToken string) (SessionTokens, error) {
	tokens, err := s.refresh.FindValidTokens(ctx)
	if err != nil {
		slog.Error("auth: find refresh tokens", "error", err)
	}
	var matched *models.RefreshToken
	for i := range tokens {
		if s.passwords.CheckToken(rawToken, tokens[i].TokenHash) {
			matched = &tokens[i]
			break
		}
	}
	if matched == nil {
		return SessionTokens{}, ErrRefreshInvalid
	}

	rotated, err := s.refresh.RevokeIfActive(ctx, matched.ID)
	if err != nil {
		return SessionTokens{}, fmt.Errorf("%w: revoke refresh token: %w", ErrSessionNotCreated, err)
	}
	if !rotated {
		return SessionTokens{}, ErrRefreshInvalid
	}
	owner, err := s.users.FindByID(ctx, matched.UserID)
	if err != nil {
		return SessionTokens{}, fmt.Errorf("%w: load user: %w", ErrSessionNotCreated, err)
	}
	if owner == nil {
		return SessionTokens{}, ErrRefreshInvalid
	}
	if err := mayHoldSession(owner); err != nil {
		return SessionTokens{}, err
	}
	session, err := s.sessions.Issue(ctx, guard, owner.ID, owner.SessionsRevokedAt)
	if err != nil {
		return SessionTokens{}, fmt.Errorf("%w: %w", ErrSessionNotCreated, err)
	}
	return session, nil
}

// Logout ends every session of the user, on every device: the session
// watermark, not the guard's per-token blacklist, because a JWT carries only
// whole-second claims and a sign-in in the second of the logout would mint
// the very token the blacklist just refused.
func (s *SignIn) Logout(ctx context.Context, userID uuid.UUID) error {
	_, err := s.revoker.RevokeAll(ctx, userID)
	return err
}

// session reads the user's accounts and issues a session with guard.
func (s *SignIn) session(ctx context.Context, guard SessionGuard, user *models.User) (SignedIn, error) {
	accounts, err := s.signInAccounts(ctx, user)
	if err != nil {
		return SignedIn{}, err
	}
	tokens, err := s.sessions.Issue(ctx, guard, user.ID, user.SessionsRevokedAt)
	if err != nil {
		return SignedIn{}, fmt.Errorf("%w: %w", ErrSessionNotCreated, err)
	}
	return SignedIn{User: user, Tokens: tokens, Accounts: accounts}, nil
}

// signInAccounts is the user's accounts and default. A failed read is logged
// and is ErrAccountsUnavailable.
func (s *SignIn) signInAccounts(ctx context.Context, user *models.User) (account.SignInAccounts, error) {
	accounts, err := s.accounts.SignInAccounts(ctx, user.ID, user.DefaultAccountID)
	if err != nil {
		slog.Error("auth: load memberships", "error", err)
		return account.SignInAccounts{}, fmt.Errorf("%w: %w", ErrAccountsUnavailable, err)
	}
	return accounts, nil
}

// mayHoldSession refuses a user whose status may not hold a session, then a
// suspended one.
func mayHoldSession(user *models.User) error {
	if !policies.UserMayHoldSession(user.Status) {
		return ErrUserInactive
	}
	if policies.UserIsSuspended(user.SuspendedAt) {
		return ErrUserSuspended
	}
	return nil
}
