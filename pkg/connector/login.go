package connector

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"

	"go.mau.fi/mautrix-imessage/pkg/bluebubbles"
)

const (
	LoginFlowIDPassword = "password"

	LoginStepIDServer   = "fi.mau.imessage.bluebubbles.server"
	LoginStepIDComplete = "fi.mau.imessage.bluebubbles.complete"
)

var (
	ErrInvalidPassword     = bridgev2.RespError{Err: "BlueBubbles rejected the password", ErrCode: "FI.MAU.IMESSAGE.INVALID_PASSWORD", StatusCode: http.StatusUnauthorized}
	ErrServerUnreachable   = bridgev2.RespError(mautrix.MUnknown.WithMessage("Couldn't reach the BlueBubbles server"))
	ErrInvalidServerURL    = bridgev2.RespError(mautrix.MInvalidParam.WithMessage("Invalid BlueBubbles server URL"))
	ErrIncompleteLoginData = bridgev2.RespError(mautrix.MInvalidParam.WithMessage("Both the server URL and the password are required"))
)

func (ic *IMConnector) GetLoginFlows() []bridgev2.LoginFlow {
	return []bridgev2.LoginFlow{{
		Name:        "BlueBubbles",
		Description: "Connect to a BlueBubbles server with its URL and password",
		ID:          LoginFlowIDPassword,
	}}
}

func (ic *IMConnector) CreateLogin(ctx context.Context, user *bridgev2.User, flowID string) (bridgev2.LoginProcess, error) {
	if flowID != LoginFlowIDPassword {
		return nil, bridgev2.ErrInvalidLoginFlowID
	}
	return &PasswordLogin{Main: ic, User: user}, nil
}

type PasswordLogin struct {
	Main *IMConnector
	User *bridgev2.User
}

var _ bridgev2.LoginProcessUserInput = (*PasswordLogin)(nil)

func (pl *PasswordLogin) Start(ctx context.Context) (*bridgev2.LoginStep, error) {
	return &bridgev2.LoginStep{
		Type:         bridgev2.LoginStepTypeUserInput,
		StepID:       LoginStepIDServer,
		Instructions: "Enter the URL and password of your BlueBubbles server",
		UserInputParams: &bridgev2.LoginUserInputParams{
			Fields: []bridgev2.LoginInputDataField{{
				Type:        bridgev2.LoginInputFieldTypeURL,
				ID:          "url",
				Name:        "Server URL",
				Description: "The address of the BlueBubbles server, e.g. http://192.168.1.10:1234",
			}, {
				Type:        bridgev2.LoginInputFieldTypePassword,
				ID:          "password",
				Name:        "Password",
				Description: "The server password configured in BlueBubbles",
			}},
		},
	}, nil
}

func (pl *PasswordLogin) Cancel() {}

func (pl *PasswordLogin) SubmitUserInput(ctx context.Context, input map[string]string) (*bridgev2.LoginStep, error) {
	serverURL := strings.TrimSpace(input["url"])
	password := input["password"]
	if serverURL == "" || password == "" {
		return nil, ErrIncompleteLoginData
	}
	client, err := bluebubbles.NewClient(serverURL, password, pl.Main.br.GetHTTPClientSettings().Compile(), pl.User.Log.With().Str("component", "bluebubbles").Logger())
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidServerURL, err)
	}
	info, err := client.GetServerInfo(ctx)
	var apiErr *bluebubbles.APIError
	if errors.As(err, &apiErr) && apiErr.IsUnauthorized() {
		return nil, ErrInvalidPassword
	} else if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrServerUnreachable, err)
	}
	loginID, remoteName, accounts := loginIdentity(client, info)
	ul, err := pl.User.NewLogin(ctx, &database.UserLogin{
		ID:         loginID,
		RemoteName: remoteName,
		Metadata: &UserLoginMetadata{
			URL:      client.URL.String(),
			Password: password,
			Accounts: accounts,
		},
	}, &bridgev2.NewLoginParams{DeleteOnConflict: true})
	if err != nil {
		return nil, err
	}
	ul.Client.Connect(ul.Log.WithContext(pl.Main.br.BackgroundCtx))
	return &bridgev2.LoginStep{
		Type:         bridgev2.LoginStepTypeComplete,
		StepID:       LoginStepIDComplete,
		Instructions: "Successfully connected to BlueBubbles",
		CompleteParams: &bridgev2.LoginCompleteParams{
			UserLoginID: ul.ID,
			UserLogin:   ul,
		},
	}, nil
}

// loginIdentity picks a stable login ID: the Mac's iCloud or iMessage account
// when BlueBubbles reports one, otherwise the server host.
func loginIdentity(client *bluebubbles.Client, info *bluebubbles.ServerInfo) (networkid.UserLoginID, string, []string) {
	var accounts []string
	for _, account := range []string{info.DetectedICloud, info.DetectedIMessage} {
		if account = normalizeAddress(account); account != "" {
			accounts = append(accounts, account)
		}
	}
	if len(accounts) > 0 {
		return networkid.UserLoginID(accounts[0]), accounts[0], accounts
	}
	return networkid.UserLoginID(client.URL.Host), client.URL.Host, nil
}
