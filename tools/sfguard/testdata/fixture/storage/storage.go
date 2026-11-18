package storage

import "errors"

type Querier interface {
	Exec(q string) error
}

type Store struct{}

func (Store) Get(id int) (string, error) { return "", errors.New("x") }

type txScope struct{}

func (txScope) Rollback() error { return nil }

func Begin() txScope { return txScope{} }

func GetUser(q Querier, id int) (string, error) { return "", nil }
func Save(q Querier, v string) error            { return nil }
func Count(q Querier) (int, bool, error)        { return 0, false, nil }
func Pure(v string) string                      { return v }
