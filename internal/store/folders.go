package store

import "fmt"

func (s *Store) CreateFolder(name string) (int64, error) { return 0, nil }

func (s *Store) RenameFolder(id int64, name string) error { return nil }

func (s *Store) RemoveFolder(id int64) error { return nil }

func (s *Store) ListFolders() ([]DeckSummary, error) { return nil, nil }

func (s *Store) FolderByRef(ref string) (*Container, error) {
	return nil, fmt.Errorf("no folder matching %q", ref)
}

func (s *Store) MoveDeckToFolder(deckID, folderID int64) error { return nil }

func (s *Store) FolderByFinish(folderID int64) ([]CollectionRow, error) { return nil, nil }
