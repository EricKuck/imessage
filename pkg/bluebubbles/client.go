package bluebubbles

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/rs/zerolog"
)

type Client struct {
	URL      *url.URL
	Password string
	HTTP     *http.Client
	Log      zerolog.Logger
}

func NewClient(baseURL, password string, httpClient *http.Client, log zerolog.Logger) (*Client, error) {
	parsed, err := url.Parse(strings.TrimRight(baseURL, "/"))
	if err != nil {
		return nil, err
	} else if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("unsupported URL scheme %q", parsed.Scheme)
	} else if parsed.Host == "" {
		return nil, errors.New("URL has no host")
	}
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &Client{URL: parsed, Password: password, HTTP: httpClient, Log: log}, nil
}

// APIError is a non-2xx response from the BlueBubbles server.
type APIError struct {
	StatusCode int
	Message    string
	Detail     string
	Data       json.RawMessage
}

func (e *APIError) Error() string {
	if e.Detail != "" {
		return fmt.Sprintf("bluebubbles: %s: %s", e.Message, e.Detail)
	}
	return fmt.Sprintf("bluebubbles: %s", e.Message)
}

func (e *APIError) IsNotFound() bool {
	return e.StatusCode == http.StatusNotFound
}

func (e *APIError) IsUnauthorized() bool {
	return e.StatusCode == http.StatusUnauthorized || e.StatusCode == http.StatusForbidden
}

// apiURL builds a request URL. Chat GUIDs like "iMessage;-;+1555" go into the
// path unescaped; url.URL takes care of the characters that actually need it,
// and BlueBubbles has been seen to 404 on percent-encoded semicolons.
func (c *Client) apiURL(path string, query url.Values) string {
	u := *c.URL
	u.Path = strings.TrimRight(c.URL.Path, "/") + "/api/v1/" + path
	u.RawPath = ""
	if query == nil {
		query = url.Values{}
	}
	query.Set("password", c.Password)
	u.RawQuery = query.Encode()
	return u.String()
}

func (c *Client) do(ctx context.Context, req *http.Request, out any) error {
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}
	var wrapper response
	if err = json.Unmarshal(body, &wrapper); err != nil {
		if resp.StatusCode >= 400 {
			return &APIError{StatusCode: resp.StatusCode, Message: http.StatusText(resp.StatusCode), Detail: string(body)}
		}
		return fmt.Errorf("failed to parse response: %w", err)
	}
	if resp.StatusCode >= 400 || (wrapper.Status != 0 && wrapper.Status >= 400) {
		apiErr := &APIError{StatusCode: resp.StatusCode, Message: wrapper.Message, Data: wrapper.Data}
		if apiErr.StatusCode < 400 {
			apiErr.StatusCode = wrapper.Status
		}
		var detail responseError
		if json.Unmarshal(wrapper.Error, &detail) == nil && detail.Message != "" {
			apiErr.Detail = detail.Message
		} else if len(wrapper.Error) > 0 {
			apiErr.Detail = string(wrapper.Error)
		}
		return apiErr
	}
	if out != nil && len(wrapper.Data) > 0 {
		if err = json.Unmarshal(wrapper.Data, out); err != nil {
			return fmt.Errorf("failed to parse response data: %w", err)
		}
	}
	return nil
}

func (c *Client) get(ctx context.Context, path string, query url.Values, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.apiURL(path, query), nil)
	if err != nil {
		return err
	}
	return c.do(ctx, req, out)
}

func (c *Client) send(ctx context.Context, method, path string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.apiURL(path, nil), reader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return c.do(ctx, req, out)
}

func (c *Client) GetServerInfo(ctx context.Context) (*ServerInfo, error) {
	var info ServerInfo
	err := c.get(ctx, "server/info", nil, &info)
	return &info, err
}

func (c *Client) GetChat(ctx context.Context, guid string) (*Chat, error) {
	var chat Chat
	err := c.get(ctx, "chat/"+guid, url.Values{"with": {"participants,lastMessage"}}, &chat)
	return &chat, err
}

// QueryChats lists chats sorted by their last message, newest first.
func (c *Client) QueryChats(ctx context.Context, limit, offset int) ([]Chat, error) {
	var chats []Chat
	err := c.send(ctx, http.MethodPost, "chat/query", &ChatQueryRequest{
		Limit:  limit,
		Offset: offset,
		With:   []string{"lastmessage"},
		Sort:   "lastmessage",
	}, &chats)
	return chats, err
}

func (c *Client) GetChatMessages(ctx context.Context, chatGUID string, query MessageQuery) ([]Message, error) {
	params := url.Values{
		"with":   {"attachment,attributedBody,handle"},
		"limit":  {strconv.Itoa(query.Limit)},
		"offset": {strconv.Itoa(query.Offset)},
		"sort":   {query.Sort},
	}
	if query.Before != nil {
		params.Set("before", strconv.FormatInt(*query.Before, 10))
	}
	if query.After != nil {
		params.Set("after", strconv.FormatInt(*query.After, 10))
	}
	var messages []Message
	err := c.get(ctx, "chat/"+chatGUID+"/message", params, &messages)
	return messages, err
}

func (c *Client) GetMessage(ctx context.Context, guid string) (*Message, error) {
	var msg Message
	err := c.get(ctx, "message/"+guid, url.Values{"with": {"chats,attachments"}}, &msg)
	return &msg, err
}

func (c *Client) GetContacts(ctx context.Context) ([]Contact, error) {
	var contacts []Contact
	err := c.get(ctx, "contact", url.Values{"extraProperties": {"avatar"}}, &contacts)
	return contacts, err
}

func (c *Client) GetHandle(ctx context.Context, address string) (*Handle, error) {
	var handle Handle
	err := c.get(ctx, "handle/"+address, nil, &handle)
	return &handle, err
}

// DownloadAttachment streams the attachment, letting the server convert
// formats Matrix clients can't show (HEIC, CAF). The caller closes the body.
func (c *Client) DownloadAttachment(ctx context.Context, guid string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.apiURL("attachment/"+guid+"/download", nil), nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, &APIError{StatusCode: resp.StatusCode, Message: http.StatusText(resp.StatusCode), Detail: string(body)}
	}
	return resp, nil
}

func (c *Client) DownloadChatIcon(ctx context.Context, chatGUID string) ([]byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.apiURL("chat/"+chatGUID+"/icon", nil), nil)
	if err != nil {
		return nil, "", err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", err
	}
	if resp.StatusCode >= 400 {
		return nil, "", &APIError{StatusCode: resp.StatusCode, Message: http.StatusText(resp.StatusCode), Detail: string(body)}
	}
	return body, resp.Header.Get("Content-Type"), nil
}

func (c *Client) SendText(ctx context.Context, req *SendTextRequest) (*Message, error) {
	var msg Message
	err := c.send(ctx, http.MethodPost, "message/text", req, &msg)
	return &msg, err
}

func (c *Client) SendAttachment(ctx context.Context, req *SendAttachmentRequest, data []byte) (*Message, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	fields := map[string]string{
		"chatGuid": req.ChatGUID,
		"tempGuid": req.TempGUID,
		"method":   req.Method,
		"name":     req.Name,
	}
	// Form fields arrive as strings, so a "false" would still be truthy.
	if req.IsAudioMessage {
		fields["isAudioMessage"] = "true"
	}
	if req.SelectedMessageGUID != "" {
		fields["selectedMessageGuid"] = req.SelectedMessageGUID
		fields["partIndex"] = strconv.Itoa(req.PartIndex)
	}
	for key, value := range fields {
		if err := writer.WriteField(key, value); err != nil {
			return nil, err
		}
	}
	part, err := writer.CreateFormFile("attachment", req.Name)
	if err != nil {
		return nil, err
	}
	if _, err = part.Write(data); err != nil {
		return nil, err
	}
	if err = writer.Close(); err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.apiURL("message/attachment", nil), &body)
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", writer.FormDataContentType())
	var msg Message
	err = c.do(ctx, httpReq, &msg)
	return &msg, err
}

func (c *Client) SendReaction(ctx context.Context, req *SendReactionRequest) (*Message, error) {
	var msg Message
	err := c.send(ctx, http.MethodPost, "message/react", req, &msg)
	return &msg, err
}

func (c *Client) EditMessage(ctx context.Context, guid string, req *EditMessageRequest) (*Message, error) {
	var msg Message
	err := c.send(ctx, http.MethodPost, "message/"+guid+"/edit", req, &msg)
	return &msg, err
}

func (c *Client) UnsendMessage(ctx context.Context, guid string, partIndex int) (*Message, error) {
	var msg Message
	err := c.send(ctx, http.MethodPost, "message/"+guid+"/unsend", &UnsendMessageRequest{PartIndex: partIndex}, &msg)
	return &msg, err
}

func (c *Client) MarkChatRead(ctx context.Context, chatGUID string) error {
	return c.send(ctx, http.MethodPost, "chat/"+chatGUID+"/read", nil, nil)
}

func (c *Client) SetTyping(ctx context.Context, chatGUID string, typing bool) error {
	method := http.MethodPost
	if !typing {
		method = http.MethodDelete
	}
	return c.send(ctx, method, "chat/"+chatGUID+"/typing", nil, nil)
}

func (c *Client) CreateChat(ctx context.Context, req *CreateChatRequest) (*Chat, error) {
	var chat Chat
	err := c.send(ctx, http.MethodPost, "chat/new", req, &chat)
	return &chat, err
}

// ParseChatGUID splits a chat GUID like "iMessage;-;+15551234567" or "SMS;+;chat123"
// into the service, whether it's a group, and the chat identifier.
func ParseChatGUID(guid string) (service string, isGroup bool, identifier string) {
	parts := strings.SplitN(guid, ";", 3)
	if len(parts) != 3 {
		return "", false, guid
	}
	return parts[0], parts[1] == "+", parts[2]
}

func MakeDMChatGUID(service, address string) string {
	return service + ";-;" + address
}
