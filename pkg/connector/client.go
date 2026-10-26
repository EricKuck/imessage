package connector

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog"
	"go.mau.fi/util/exsync"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/bridgev2/simplevent"
	"maunium.net/go/mautrix/bridgev2/status"

	"go.mau.fi/mautrix-imessage/pkg/bluebubbles"
)

type IMClient struct {
	Main      *IMConnector
	UserLogin *bridgev2.UserLogin
	Meta      *UserLoginMetadata
	Client    *bluebubbles.Client

	privateAPI atomic.Bool
	stopSocket atomic.Pointer[context.CancelFunc]
	connected  atomic.Bool

	// Matrix sends in flight. Our own messages echo back over the socket, often
	// before the send call returns, so echoes wait for the send to be saved.
	pendingSends atomic.Int32
	// GUIDs of messages the bridge sent as side effects (caption follow-ups)
	// whose echoes must not be bridged back.
	ignoredEchoes *exsync.Map[string, time.Time]

	contacts contactCache
}

var (
	_ bridgev2.NetworkAPI                    = (*IMClient)(nil)
	_ bridgev2.EditHandlingNetworkAPI        = (*IMClient)(nil)
	_ bridgev2.ReactionHandlingNetworkAPI    = (*IMClient)(nil)
	_ bridgev2.RedactionHandlingNetworkAPI   = (*IMClient)(nil)
	_ bridgev2.ReadReceiptHandlingNetworkAPI = (*IMClient)(nil)
	_ bridgev2.TypingHandlingNetworkAPI      = (*IMClient)(nil)
	_ bridgev2.BackfillingNetworkAPI         = (*IMClient)(nil)
	_ bridgev2.IdentifierResolvingNetworkAPI = (*IMClient)(nil)
	_ bridgev2.ContactListingNetworkAPI      = (*IMClient)(nil)
)

func (ic *IMConnector) LoadUserLogin(ctx context.Context, login *bridgev2.UserLogin) error {
	meta := login.Metadata.(*UserLoginMetadata)
	client := &IMClient{
		Main:          ic,
		UserLogin:     login,
		Meta:          meta,
		ignoredEchoes: exsync.NewMap[string, time.Time](),
	}
	if meta.URL != "" {
		var err error
		client.Client, err = bluebubbles.NewClient(meta.URL, meta.Password, ic.br.GetHTTPClientSettings().Compile(), login.Log.With().Str("component", "bluebubbles").Logger())
		if err != nil {
			login.Log.Err(err).Msg("Failed to create BlueBubbles client from saved login")
		}
	}
	login.Client = client
	return nil
}

func (c *IMClient) Connect(ctx context.Context) {
	if c.Client == nil {
		c.UserLogin.BridgeState.Send(status.BridgeState{
			StateEvent: status.StateBadCredentials,
			Error:      IMNotLoggedIn,
			UserAction: status.UserActionRelogin,
		})
		return
	}
	c.Disconnect()
	socketCtx, cancel := context.WithCancel(c.UserLogin.Log.WithContext(c.Main.br.BackgroundCtx))
	c.stopSocket.Store(&cancel)
	c.UserLogin.BridgeState.Send(status.BridgeState{StateEvent: status.StateConnecting})
	go c.Client.RunSocket(socketCtx, c.handleSocketEvent, bluebubbles.SocketCallbacks{
		OnConnect:    func() { c.onSocketConnected(socketCtx) },
		OnDisconnect: c.onSocketDisconnected,
	})
}

func (c *IMClient) onSocketConnected(ctx context.Context) {
	log := zerolog.Ctx(ctx)
	info, err := c.Client.GetServerInfo(ctx)
	if err != nil {
		log.Err(err).Msg("Failed to get server info after connecting")
	} else {
		c.privateAPI.Store(info.PrivateAPI)
		if !info.PrivateAPI {
			log.Warn().Msg("BlueBubbles private API is disabled, reactions, edits, read receipts and typing won't work")
		}
		c.updateAccounts(ctx, info)
	}
	c.connected.Store(true)
	c.UserLogin.BridgeState.Send(status.BridgeState{StateEvent: status.StateConnected})
	go c.syncChats(ctx)
}

func (c *IMClient) updateAccounts(ctx context.Context, info *bluebubbles.ServerInfo) {
	_, _, accounts := loginIdentity(c.Client, info)
	if len(accounts) == 0 || equalStrings(accounts, c.Meta.Accounts) {
		return
	}
	c.Meta.Accounts = accounts
	if err := c.UserLogin.Save(ctx); err != nil {
		zerolog.Ctx(ctx).Err(err).Msg("Failed to save login accounts")
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func (c *IMClient) onSocketDisconnected(err error) {
	c.connected.Store(false)
	state := status.BridgeState{
		StateEvent: status.StateTransientDisconnect,
		Error:      IMDisconnected,
		Info:       map[string]any{"go_error": err.Error()},
	}
	if errors.Is(err, bluebubbles.ErrSocketRejected) {
		state.StateEvent = status.StateBadCredentials
		state.Error = IMBadCredentials
		state.UserAction = status.UserActionRelogin
	}
	c.UserLogin.BridgeState.Send(state)
}

// syncChats creates portals for the most recent chats and backfills them.
func (c *IMClient) syncChats(ctx context.Context) {
	count := c.Main.Config.InitialChatSyncCount
	if count <= 0 {
		return
	}
	log := zerolog.Ctx(ctx).With().Str("action", "sync chats").Logger()
	chats, err := c.Client.QueryChats(ctx, count, 0)
	if err != nil {
		log.Err(err).Msg("Failed to list chats")
		return
	}
	for _, chat := range chats {
		if chat.LastMessage == nil {
			continue
		}
		c.Main.br.QueueRemoteEvent(c.UserLogin, &simplevent.ChatResync{
			EventMeta: simplevent.EventMeta{
				Type:         bridgev2.RemoteEventChatResync,
				LogContext:   chatLogContext(chat.GUID),
				PortalKey:    c.makePortalKey(chat.GUID),
				CreatePortal: true,
			},
			GetChatInfoFunc: c.GetChatInfo,
			LatestMessageTS: time.UnixMilli(chat.LastMessage.DateCreated),
		})
	}
}

func chatLogContext(chatGUID string) func(zerolog.Context) zerolog.Context {
	return func(c zerolog.Context) zerolog.Context {
		return c.Str("chat_guid", chatGUID)
	}
}

func (c *IMClient) Disconnect() {
	if cancel := c.stopSocket.Swap(nil); cancel != nil {
		(*cancel)()
	}
	c.connected.Store(false)
}

func (c *IMClient) IsLoggedIn() bool {
	return c.Client != nil
}

// LogoutRemote has nothing to invalidate: the bridge only holds the server password.
func (c *IMClient) LogoutRemote(ctx context.Context) {
	c.Disconnect()
}

func (c *IMClient) IsThisUser(ctx context.Context, userID networkid.UserID) bool {
	if userID == c.selfUserID() {
		return true
	}
	for _, account := range c.Meta.Accounts {
		if networkid.UserID(account) == userID {
			return true
		}
	}
	return false
}

func (c *IMClient) sendMethod() string {
	if c.privateAPI.Load() {
		return bluebubbles.MethodPrivateAPI
	}
	return bluebubbles.MethodAppleScript
}

const pendingSendWait = 5 * time.Second

func (c *IMClient) waitForPendingSends() {
	deadline := time.Now().Add(pendingSendWait)
	for c.pendingSends.Load() > 0 && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
	}
}
