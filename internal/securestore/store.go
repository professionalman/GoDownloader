package securestore

import (
	"context"
	"errors"
)

type Repository interface {
	GetSecret(ctx context.Context, scope, owner, field string) ([]byte, error)
	SetSecret(ctx context.Context, scope, owner, field string, ciphertext []byte) error
	DeleteSecret(ctx context.Context, scope, owner, field string) error
	HasSecret(ctx context.Context, scope, owner, field string) (bool, error)
}

type Store struct {
	repo   Repository
	cipher *Cipher
}

func NewStore(repo Repository, cipher *Cipher) *Store {
	return &Store{repo: repo, cipher: cipher}
}

func (s *Store) Available() bool { return s != nil && s.cipher != nil && s.cipher.Available() }

func (s *Store) Put(ctx context.Context, scope, owner, field, value string) error {
	if s == nil || s.repo == nil || !s.Available() {
		return ErrUnavailable
	}
	sealed, err := s.cipher.Encrypt(scope, owner, field, value)
	if err != nil {
		return err
	}
	return s.repo.SetSecret(ctx, scope, owner, field, sealed)
}

func (s *Store) Get(ctx context.Context, scope, owner, field string) (string, error) {
	if s == nil || s.repo == nil {
		return "", ErrUnavailable
	}
	sealed, err := s.repo.GetSecret(ctx, scope, owner, field)
	if err != nil {
		return "", err
	}
	if len(sealed) == 0 {
		return "", nil
	}
	return s.cipher.Decrypt(scope, owner, field, sealed)
}

func (s *Store) Delete(ctx context.Context, scope, owner, field string) error {
	if s == nil || s.repo == nil {
		return ErrUnavailable
	}
	return s.repo.DeleteSecret(ctx, scope, owner, field)
}

func (s *Store) Has(ctx context.Context, scope, owner, field string) (bool, error) {
	if s == nil || s.repo == nil {
		return false, ErrUnavailable
	}
	return s.repo.HasSecret(ctx, scope, owner, field)
}

func IsUnavailable(err error) bool { return errors.Is(err, ErrUnavailable) }
