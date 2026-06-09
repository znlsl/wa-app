package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	waappv1 "github.com/byte-v-forge/wa-app/gen/go/byte/v/forge/waapp/v1"
)

const contactProfilePictureCacheTTL = 6 * time.Hour

type waContactProfilePictureResolver interface {
	ResolveContactProfilePicture(context.Context, EngineContactProfilePictureInput) EngineContactProfilePictureResult
}

type WAContactProfilePicture struct {
	ProfilePictureID string
	ContentType      string
	Data             []byte
}

type contactProfilePictureCacheEntry struct {
	ContentType string `json:"content_type"`
	Data        []byte `json:"data"`
}

func (s *Server) GetWAContactProfilePicture(ctx context.Context, contactID string) (WAContactProfilePicture, error) {
	contactID = strings.TrimSpace(contactID)
	if contactID == "" {
		return WAContactProfilePicture{}, NewError(waappv1.WaErrorCode_WA_ERROR_CODE_VALIDATION_FAILED, "WA contact id is required", false)
	}
	contact, err := s.store.GetWAContact(ctx, contactID)
	if err != nil {
		return WAContactProfilePicture{}, err
	}
	if contact.GetProfilePictureId() == "" {
		return WAContactProfilePicture{}, NewError(waappv1.WaErrorCode_WA_ERROR_CODE_MESSAGE_NOT_FOUND, "WA profile picture not found", false)
	}
	if cached, ok := s.cachedWAContactProfilePicture(ctx, contact); ok {
		return cached, nil
	}
	loginState, err := s.activeContactResolveLoginState(ctx, contact.GetWaAccountId())
	if err != nil {
		return WAContactProfilePicture{}, err
	}
	runner, release, err := s.contactResolverRunner(ctx, &waappv1.RequestContext{})
	if err != nil {
		return WAContactProfilePicture{}, err
	}
	defer release()
	resolver, ok := runner.(waContactProfilePictureResolver)
	if !ok {
		return WAContactProfilePicture{}, NewError(waappv1.WaErrorCode_WA_ERROR_CODE_UNSUPPORTED_OPERATION, "WA contact profile picture resolver is not configured", false)
	}
	result := resolver.ResolveContactProfilePicture(ctx, EngineContactProfilePictureInput{
		WAAccountID:          contact.GetWaAccountId(),
		ClientProfileID:      loginState.GetClientProfileId(),
		RegisteredIdentityID: loginState.GetRegisteredIdentityId(),
		ContactJID:           contact.GetJid(),
		RemoteTimeout:        defaultContactProfilePictureTimeout,
	})
	if result.Err != nil {
		return WAContactProfilePicture{}, result.Err
	}
	if result.ProfilePictureID != "" && result.ProfilePictureID != contact.GetProfilePictureId() {
		contact.ProfilePictureId = result.ProfilePictureID
		_ = s.store.SaveWAContacts(ctx, []*waappv1.WAContact{contact})
	}
	picture := WAContactProfilePicture{
		ProfilePictureID: firstNonEmpty(result.ProfilePictureID, contact.GetProfilePictureId()),
		ContentType:      result.ContentType,
		Data:             result.Data,
	}
	s.cacheWAContactProfilePicture(ctx, contact.GetContactId(), picture)
	return picture, nil
}

func (s *Server) cachedWAContactProfilePicture(ctx context.Context, contact *waappv1.WAContact) (WAContactProfilePicture, bool) {
	if s == nil || s.runtime == nil || contact == nil || contact.GetProfilePictureId() == "" {
		return WAContactProfilePicture{}, false
	}
	data, err := s.runtime.GetTransientState(ctx, contactProfilePictureCacheKey(contact.GetContactId(), contact.GetProfilePictureId()))
	if err != nil || len(data) == 0 {
		return WAContactProfilePicture{}, false
	}
	var entry contactProfilePictureCacheEntry
	if json.Unmarshal(data, &entry) != nil || len(entry.Data) == 0 || entry.ContentType == "" {
		return WAContactProfilePicture{}, false
	}
	return WAContactProfilePicture{ProfilePictureID: contact.GetProfilePictureId(), ContentType: entry.ContentType, Data: entry.Data}, true
}

func (s *Server) cacheWAContactProfilePicture(ctx context.Context, contactID string, picture WAContactProfilePicture) {
	if s == nil || s.runtime == nil || contactID == "" || picture.ProfilePictureID == "" || len(picture.Data) == 0 {
		return
	}
	data, err := json.Marshal(contactProfilePictureCacheEntry{ContentType: picture.ContentType, Data: picture.Data})
	if err != nil {
		return
	}
	_ = s.runtime.SaveTransientState(ctx, contactProfilePictureCacheKey(contactID, picture.ProfilePictureID), data, contactProfilePictureCacheTTL)
}

func contactProfilePictureCacheKey(contactID string, profilePictureID string) string {
	return "wa-contact-profile-picture:" + contactID + ":" + profilePictureID
}

func IsWAContactProfilePictureNotFound(err error) bool {
	var appErr *AppError
	return errors.As(err, &appErr) && appErr.Code == waappv1.WaErrorCode_WA_ERROR_CODE_MESSAGE_NOT_FOUND
}
