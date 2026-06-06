package recipestore

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/transport"
	"github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/go-git/go-git/v5/plumbing/transport/ssh"
)

// GitRecipeStore implements RecipeStore by pulling, committing, and pushing from/to a remote Git repository.
type GitRecipeStore struct {
	url       string
	branch    string
	username  string
	password  string
	sshKey    string
	localPath string
}

// NewGitRecipeStore creates a GitRecipeStore with configured Git credentials and target repository.
func NewGitRecipeStore(url, branch, username, password, sshKey, localPath string) *GitRecipeStore {
	if branch == "" {
		branch = "main"
	}
	return &GitRecipeStore{
		url:       url,
		branch:    branch,
		username:  username,
		password:  password,
		sshKey:    sshKey,
		localPath: localPath,
	}
}

func (s *GitRecipeStore) getAuth() (transport.AuthMethod, error) {
	if s.sshKey != "" {
		publicKeys, err := ssh.NewPublicKeysFromFile("git", s.sshKey, "")
		if err != nil {
			return nil, err
		}
		return publicKeys, nil
	}
	if s.username != "" || s.password != "" {
		return &http.BasicAuth{
			Username: s.username,
			Password: s.password,
		}, nil
	}
	return nil, nil
}

func (s *GitRecipeStore) ensureClone(ctx context.Context) (*git.Repository, error) {
	auth, err := s.getAuth()
	if err != nil {
		return nil, err
	}

	r, err := git.PlainOpen(s.localPath)
	if err == nil {
		w, err := r.Worktree()
		if err != nil {
			return r, nil
		}
		_ = w.PullContext(ctx, &git.PullOptions{
			Auth:          auth,
			ReferenceName: plumbing.NewBranchReferenceName(s.branch),
			SingleBranch:  true,
		})
		return r, nil
	}

	if err != git.ErrRepositoryNotExists {
		return nil, err
	}

	if err := os.MkdirAll(s.localPath, 0o700); err != nil {
		return nil, err
	}

	return git.PlainCloneContext(ctx, s.localPath, false, &git.CloneOptions{
		URL:           s.url,
		ReferenceName: plumbing.NewBranchReferenceName(s.branch),
		SingleBranch:  true,
		Auth:          auth,
	})
}

// List pulls changes and returns metadata of all recipe files inside the Git repository workspace.
func (s *GitRecipeStore) List(ctx context.Context) ([]RecipeMetadata, error) {
	_, err := s.ensureClone(ctx)
	if err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(s.localPath)
	if err != nil {
		return nil, err
	}

	var list []RecipeMetadata
	for _, entry := range entries {
		if entry.Type().IsRegular() && strings.HasSuffix(strings.ToLower(entry.Name()), ".cue") {
			info, err := entry.Info()
			if err != nil {
				continue
			}
			list = append(list, RecipeMetadata{
				Name:       entry.Name(),
				Path:       filepath.Join(s.localPath, entry.Name()),
				ModifiedAt: info.ModTime().Unix(),
				Size:       info.Size(),
			})
		}
	}
	return list, nil
}

// Get pulls changes and returns the raw CUE content of a specific recipe inside the Git workspace.
func (s *GitRecipeStore) Get(ctx context.Context, name string) (string, error) {
	_, err := s.ensureClone(ctx)
	if err != nil {
		return "", err
	}

	name, err = normalizeCueRecipeName(name)
	if err != nil {
		return "", err
	}
	path := filepath.Join(s.localPath, name)
	// #nosec G304
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// Save writes content to a recipe file, commits the change with a generic message, and pushes to remote Git repo.
func (s *GitRecipeStore) Save(ctx context.Context, name string, content string) error {
	r, err := s.ensureClone(ctx)
	if err != nil {
		return err
	}

	name, err = normalizeCueRecipeName(name)
	if err != nil {
		return err
	}
	path := filepath.Join(s.localPath, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		return err
	}

	w, err := r.Worktree()
	if err != nil {
		return err
	}

	_, err = w.Add(name)
	if err != nil {
		return err
	}

	_, err = w.Commit("Save recipe: "+name, &git.CommitOptions{
		Author: &object.Signature{
			Name:  "Honey Studio",
			Email: "studio@honey.local",
			When:  time.Now(),
		},
	})
	if err != nil {
		return err
	}

	auth, err := s.getAuth()
	if err != nil {
		return err
	}

	return r.PushContext(ctx, &git.PushOptions{
		Auth: auth,
	})
}

// Delete removes a recipe file, commits the deletion, and pushes changes to the remote Git repository.
func (s *GitRecipeStore) Delete(ctx context.Context, name string) error {
	r, err := s.ensureClone(ctx)
	if err != nil {
		return err
	}

	name, err = normalizeCueRecipeName(name)
	if err != nil {
		return err
	}
	path := filepath.Join(s.localPath, name)
	err = os.Remove(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	w, err := r.Worktree()
	if err != nil {
		return err
	}

	_, err = w.Add(name)
	if err != nil {
		return err
	}

	_, err = w.Commit("Delete recipe: "+name, &git.CommitOptions{
		Author: &object.Signature{
			Name:  "Honey Studio",
			Email: "studio@honey.local",
			When:  time.Now(),
		},
	})
	if err != nil {
		return err
	}

	auth, err := s.getAuth()
	if err != nil {
		return err
	}

	return r.PushContext(ctx, &git.PushOptions{
		Auth: auth,
	})
}
