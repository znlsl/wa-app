package app

import (
	"context"
	"errors"
	"log"
	"time"

	"github.com/byte-v-forge/common-lib/accountcrud"
	accountv1 "github.com/byte-v-forge/common-lib/gen/go/byte/v/forge/contracts/account/v1"
	waappv1 "github.com/byte-v-forge/wa-app/gen/go/byte/v/forge/waapp/v1"
)

type waAccountStore struct {
	store       Store
	workspaceID string
}

func (s *Server) waAccounts(workspaceID string) *accountcrud.Manager[*waappv1.WAAccount] {
	store := waAccountStore{store: s.store, workspaceID: workspaceID}
	return accountcrud.New[*waappv1.WAAccount](accountcrud.Config[*waappv1.WAAccount]{
		Store: accountcrud.StoreFuncs[*waappv1.WAAccount]{
			Name:       "wa account store",
			ListFunc:   store.list,
			GetFunc:    store.get,
			UpsertFunc: store.upsert,
			DeleteFunc: store.delete,
		},
		Descriptor: waAccountDescriptor,
		AccountOf: func(account *waappv1.WAAccount) *accountv1.Account {
			if account == nil {
				return nil
			}
			return account.GetAccount()
		},
		Publishers: s.waAccountPublishers(),
		IDField:    "wa_account_id",
	})
}

func (s *Server) saveWAAccount(ctx context.Context, workspaceID string, account *waappv1.WAAccount) (*waappv1.WAAccount, error) {
	return s.waAccounts(workspaceID).Upsert(ctx, account)
}

func (s *Server) getWAAccount(ctx context.Context, workspaceID string, accountID string) (*waappv1.WAAccount, error) {
	account, found, err := s.waAccounts(workspaceID).Get(ctx, accountID)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, NewError(waappv1.WaErrorCode_WA_ERROR_CODE_WA_ACCOUNT_NOT_FOUND, "WA account not found", false)
	}
	return account, nil
}

func (s *Server) listWAAccounts(ctx context.Context, workspaceID string, cursor string, limit int) ([]*waappv1.WAAccount, string, error) {
	page, err := s.waAccounts(workspaceID).List(ctx, accountcrud.ListRequest{Cursor: cursor, Limit: limit})
	if err != nil {
		return nil, "", err
	}
	return page.Records, page.NextCursor, nil
}

func (s waAccountStore) list(ctx context.Context, req accountcrud.ListRequest) (accountcrud.Page[*waappv1.WAAccount], error) {
	accounts, nextCursor, err := s.store.ListWAAccounts(ctx, s.workspaceID, req.Cursor, req.Limit)
	if err != nil {
		return accountcrud.Page[*waappv1.WAAccount]{}, err
	}
	return accountcrud.Page[*waappv1.WAAccount]{Records: accounts, NextCursor: nextCursor}, nil
}

func (s waAccountStore) get(ctx context.Context, accountID string) (*waappv1.WAAccount, bool, error) {
	account, err := s.store.GetWAAccount(ctx, s.workspaceID, accountID)
	if isWAAccountNotFound(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return account, true, nil
}

func (s waAccountStore) upsert(ctx context.Context, account *waappv1.WAAccount) (*waappv1.WAAccount, error) {
	if err := s.store.SaveWAAccount(ctx, account); err != nil {
		return nil, err
	}
	return s.store.GetWAAccount(ctx, s.workspaceID, waAccountID(account))
}

func (s waAccountStore) delete(context.Context, string) (*waappv1.WAAccount, bool, error) {
	return nil, false, NewError(waappv1.WaErrorCode_WA_ERROR_CODE_UNSUPPORTED_OPERATION, "WA account delete is not supported", false)
}

func (s *Server) waAccountPublishers() []accountcrud.ChangePublisher {
	if s == nil || s.accountPublisher == nil {
		return nil
	}
	publisher := accountcrud.WrapPublisher(accountcrud.FromEventBusPublisher(s.accountPublisher), accountcrud.PublisherOptions{
		Detached:     true,
		Timeout:      5 * time.Second,
		IgnoreErrors: true,
		OnError: func(_ context.Context, _ accountv1.AccountChangeKind, account *accountv1.Account, err error) {
			if account != nil {
				log.Printf("publish WA account event failed account=%s: %v", account.GetKey().GetAccountId(), sanitizeEventPublishError(err))
			}
		},
	})
	return []accountcrud.ChangePublisher{publisher}
}

func isWAAccountNotFound(err error) bool {
	var appErr *AppError
	return errors.As(err, &appErr) && appErr.Code == waappv1.WaErrorCode_WA_ERROR_CODE_WA_ACCOUNT_NOT_FOUND
}
