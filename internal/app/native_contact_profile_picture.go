package app

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	waappv1 "github.com/byte-v-forge/wa-app/gen/go/byte/v/forge/waapp/v1"
)

const (
	defaultContactProfilePictureTimeout = 20 * time.Second
	profilePictureDirectPathHost        = "https://pps.whatsapp.net"
	profilePictureDownloadMaxBytes      = 2 << 20
)

type contactProfilePictureLocation struct {
	ID         string
	DirectPath string
	URL        string
	InlineData []byte
}

func (e *NativeEngine) ResolveContactProfilePicture(ctx context.Context, input EngineContactProfilePictureInput) EngineContactProfilePictureResult {
	jid := normalizeWAJID(input.ContactJID)
	if input.WAAccountID == "" || input.ClientProfileID == "" || input.RegisteredIdentityID == "" || jid == "" {
		return EngineContactProfilePictureResult{Err: NewError(waappv1.WaErrorCode_WA_ERROR_CODE_VALIDATION_FAILED, "WA contact profile picture input is incomplete", false)}
	}
	state, err := e.loadState(ctx, input.ClientProfileID)
	if err != nil {
		return EngineContactProfilePictureResult{Err: err}
	}
	state.ensureMaps()
	if state.ChatStatic.Private == "" || state.ChatStatic.Public == "" {
		state.ChatStatic = ensureChatStatic(state.ChatStatic)
		if err := e.saveState(ctx, input.ClientProfileID, state); err != nil {
			return EngineContactProfilePictureResult{Err: err}
		}
	}
	timeout := input.RemoteTimeout
	if timeout <= 0 {
		timeout = defaultContactProfilePictureTimeout
	}
	proxyURL, err := e.proxyURL()
	if err != nil {
		return EngineContactProfilePictureResult{Err: err}
	}
	client := newChatdClient(chatdConfigForState(proxyURL, state, timeout))
	location, update, err := e.contactProfilePictureLocationFromUsync(ctx, client, state, input, jid)
	if applyChatdConnectionState(&state, update) {
		_ = e.saveState(ctx, input.ClientProfileID, state)
	}
	if err != nil {
		location, update, err = e.contactProfilePictureLocationFromProfileIQ(ctx, client, state, input, jid)
		if applyChatdConnectionState(&state, update) {
			_ = e.saveState(ctx, input.ClientProfileID, state)
		}
		if err != nil {
			return EngineContactProfilePictureResult{Err: err}
		}
	}
	if len(location.InlineData) > 0 {
		contentType, err := profilePictureContentType(location.InlineData, "")
		return EngineContactProfilePictureResult{ProfilePictureID: location.ID, ContentType: contentType, Data: location.InlineData, Err: err}
	}
	httpClient, err := e.httpForProxy()
	if err != nil {
		return EngineContactProfilePictureResult{Err: err}
	}
	data, contentType, err := httpClient.getProfilePicture(ctx, location, state.UserAgent)
	return EngineContactProfilePictureResult{ProfilePictureID: location.ID, ContentType: contentType, Data: data, Err: err}
}

func (e *NativeEngine) contactProfilePictureLocationFromUsync(ctx context.Context, client *chatdClient, state nativeState, input EngineContactProfilePictureInput, jid string) (contactProfilePictureLocation, chatdSessionUpdate, error) {
	variant := contactUsyncVariant{
		Name:           "profile_picture_usync",
		Context:        "interactive",
		UserContainer:  "list",
		UserAddressing: contactUsyncUserLID,
		Query: []chatdNode{
			{Tag: "contact", Attrs: map[string]string{"addressing_mode": "lid"}},
			{Tag: "lid"},
			buildContactUsyncPictureQuery(),
		},
	}
	ref := contactUsyncRef{QueryJID: jid, FallbackLID: jid}
	request := buildContactUsyncIQ(e.ids.NewID("wappic_usync_"), e.ids.NewID("sync_sid_picture_"), []contactUsyncRef{ref}, variant)
	response, update, err := client.sendIQ(ctx, state, input.RegisteredIdentityID, defaultWAAppVersion, request, "profile picture usync iq timed out")
	if err != nil {
		return contactProfilePictureLocation{}, update, err
	}
	usync, ok := findChatdNode(response, "usync")
	if !ok {
		return contactProfilePictureLocation{}, update, NewError(waappv1.WaErrorCode_WA_ERROR_CODE_MESSAGE_NOT_FOUND, "WA profile picture not found", false)
	}
	for _, listTag := range []string{"list", "side_list"} {
		if listNode, ok := chatdChild(usync, listTag); ok {
			if picture, ok := findChatdNode(listNode, "picture"); ok {
				location, err := contactProfilePictureLocationFromPicture(picture)
				return location, update, err
			}
		}
	}
	return contactProfilePictureLocation{}, update, NewError(waappv1.WaErrorCode_WA_ERROR_CODE_MESSAGE_NOT_FOUND, "WA profile picture not found", false)
}

func (e *NativeEngine) contactProfilePictureLocationFromProfileIQ(ctx context.Context, client *chatdClient, state nativeState, input EngineContactProfilePictureInput, jid string) (contactProfilePictureLocation, chatdSessionUpdate, error) {
	request := buildContactProfilePictureIQ(e.ids.NewID("wappic_"), jid, "preview")
	response, update, err := client.sendIQ(ctx, state, input.RegisteredIdentityID, defaultWAAppVersion, request, "profile picture iq timed out")
	if err != nil {
		return contactProfilePictureLocation{}, update, err
	}
	location, err := contactProfilePictureLocationFromIQ(response)
	return location, update, err
}

func buildContactProfilePictureIQ(id string, jid string, pictureType string) chatdNode {
	return chatdNode{
		Tag:   "iq",
		Attrs: map[string]string{"xmlns": "w:profile:picture", "id": id, "type": "get", "target": normalizeWAJID(jid)},
		Content: []chatdNode{{
			Tag:   "picture",
			Attrs: map[string]string{"type": firstNonEmpty(pictureType, "preview")},
		}},
	}
}

func contactProfilePictureLocationFromIQ(response chatdNode) (contactProfilePictureLocation, error) {
	if response.Attrs["type"] == "error" {
		return contactProfilePictureLocation{}, NewError(waappv1.WaErrorCode_WA_ERROR_CODE_REJECTED, "WA profile picture request was rejected", false)
	}
	picture, ok := findChatdNode(response, "picture")
	if !ok {
		return contactProfilePictureLocation{}, NewError(waappv1.WaErrorCode_WA_ERROR_CODE_MESSAGE_NOT_FOUND, "WA profile picture not found", false)
	}
	return contactProfilePictureLocationFromPicture(picture)
}

func contactProfilePictureLocationFromPicture(picture chatdNode) (contactProfilePictureLocation, error) {
	status := strings.TrimSpace(picture.Attrs["status"])
	if status != "" && status != "200" && status != "ok" {
		return contactProfilePictureLocation{}, NewError(waappv1.WaErrorCode_WA_ERROR_CODE_MESSAGE_NOT_FOUND, "WA profile picture not found", false)
	}
	location := contactProfilePictureLocation{
		ID:         contactProfilePictureID(picture),
		DirectPath: strings.TrimSpace(picture.Attrs["direct_path"]),
		URL:        strings.TrimSpace(picture.Attrs["url"]),
	}
	if data, ok := picture.Content.([]byte); ok && len(data) > 0 {
		location.InlineData = data
	}
	if len(location.InlineData) == 0 && location.DirectPath == "" && location.URL == "" {
		return contactProfilePictureLocation{}, NewError(waappv1.WaErrorCode_WA_ERROR_CODE_MESSAGE_NOT_FOUND, "WA profile picture not found", false)
	}
	return location, nil
}

func (c *nativeHTTPClient) getProfilePicture(ctx context.Context, location contactProfilePictureLocation, userAgent string) ([]byte, string, error) {
	endpoint, err := profilePictureDownloadURL(location)
	if err != nil {
		return nil, "", err
	}
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		if attempt > 0 {
			if err := sleepWithContext(ctx, 250*time.Millisecond); err != nil {
				return nil, "", err
			}
		}
		data, contentType, err := c.getProfilePictureOnce(ctx, endpoint, userAgent)
		if err == nil {
			return data, contentType, nil
		}
		lastErr = err
		if !profilePictureDownloadRetryable(err) {
			break
		}
	}
	return nil, "", lastErr
}

func (c *nativeHTTPClient) getProfilePictureOnce(ctx context.Context, endpoint string, userAgent string) ([]byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("User-Agent", firstNonEmpty(userAgent, nativeUserAgent(defaultWAAppVersion)))
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	data, err := readLimitedProfilePicture(resp.Body)
	if err != nil {
		return nil, "", err
	}
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone {
		return nil, "", NewError(waappv1.WaErrorCode_WA_ERROR_CODE_MESSAGE_NOT_FOUND, "WA profile picture not found", false)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, "", profilePictureHTTPError(resp.StatusCode)
	}
	contentType, err := profilePictureContentType(data, resp.Header.Get("Content-Type"))
	if err != nil {
		return nil, "", err
	}
	return data, contentType, nil
}

func profilePictureDownloadURL(location contactProfilePictureLocation) (string, error) {
	for _, candidate := range []string{location.URL, location.DirectPath} {
		endpoint, ok := normalizeProfilePictureURL(candidate)
		if ok {
			return endpoint, nil
		}
	}
	return "", NewError(waappv1.WaErrorCode_WA_ERROR_CODE_MESSAGE_NOT_FOUND, "WA profile picture not found", false)
}

func normalizeProfilePictureURL(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", false
	}
	if strings.HasPrefix(value, "/") {
		return profilePictureDirectPathHost + value, true
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed == nil {
		return "", false
	}
	if parsed.Scheme == "" && strings.HasPrefix(parsed.Path, "/") {
		return profilePictureDirectPathHost + parsed.String(), true
	}
	if (parsed.Scheme != "https" && parsed.Scheme != "http") || !profilePictureURLHostAllowed(parsed.Host) {
		return "", false
	}
	return parsed.String(), true
}

func profilePictureURLHostAllowed(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	if cut, _, ok := strings.Cut(host, ":"); ok {
		host = cut
	}
	return host == "whatsapp.net" || strings.HasSuffix(host, ".whatsapp.net") || host == "fbcdn.net" || strings.HasSuffix(host, ".fbcdn.net")
}

func readLimitedProfilePicture(reader io.Reader) ([]byte, error) {
	var buf bytes.Buffer
	limited := io.LimitReader(reader, profilePictureDownloadMaxBytes+1)
	if _, err := buf.ReadFrom(limited); err != nil {
		return nil, err
	}
	data := buf.Bytes()
	if len(data) > profilePictureDownloadMaxBytes {
		return nil, NewError(waappv1.WaErrorCode_WA_ERROR_CODE_REJECTED, "WA profile picture is too large", false)
	}
	return data, nil
}

func profilePictureContentType(data []byte, header string) (string, error) {
	contentType := strings.ToLower(strings.TrimSpace(strings.Split(header, ";")[0]))
	if profilePictureContentTypeAllowed(contentType) {
		return contentType, nil
	}
	detected := strings.ToLower(http.DetectContentType(data))
	if profilePictureContentTypeAllowed(detected) {
		return detected, nil
	}
	return "", NewError(waappv1.WaErrorCode_WA_ERROR_CODE_REJECTED, "WA profile picture content type is not supported", false)
}

func profilePictureContentTypeAllowed(contentType string) bool {
	switch contentType {
	case "image/jpeg", "image/png", "image/webp":
		return true
	default:
		return false
	}
}

func profilePictureHTTPError(statusCode int) error {
	retryable := statusCode == http.StatusTooManyRequests || statusCode >= http.StatusInternalServerError
	return NewError(waappv1.WaErrorCode_WA_ERROR_CODE_ROUTE_UNAVAILABLE, fmt.Sprintf("WA profile picture download failed with HTTP %d", statusCode), retryable)
}

func profilePictureDownloadRetryable(err error) bool {
	appErr, ok := err.(*AppError)
	if !ok {
		return true
	}
	return appErr.Retryable
}

func sleepWithContext(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
