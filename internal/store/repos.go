package store

import (
	"database/sql"
	"fmt"
)

const repoCols = `id, name, path, default_branch, created_at`

func scanRepo(r interface{ Scan(...any) error }) (Repo, error) {
	var x Repo
	err := r.Scan(&x.ID, &x.Name, &x.Path, &x.DefaultBranch, &x.CreatedAt)
	return x, err
}

// AddRepo registers a repo; name and path must be unique.
func AddRepo(db *sql.DB, r Repo) (Repo, error) {
	r.CreatedAt = now()
	res, err := db.Exec(`INSERT INTO repos (name, path, default_branch, created_at) VALUES (?,?,?,?)`,
		r.Name, r.Path, r.DefaultBranch, r.CreatedAt)
	if err != nil {
		return Repo{}, fmt.Errorf("add repo %q: %w", r.Name, err)
	}
	r.ID, _ = res.LastInsertId()
	return r, nil
}

// ListRepos returns all repos ordered by name.
func ListRepos(db *sql.DB) ([]Repo, error) {
	rows, err := db.Query(`SELECT ` + repoCols + ` FROM repos ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("list repos: %w", err)
	}
	defer rows.Close()
	var out []Repo
	for rows.Next() {
		r, err := scanRepo(rows)
		if err != nil {
			return nil, fmt.Errorf("scan repo: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// GetRepo returns the repo by ID or a wrapped sql.ErrNoRows.
func GetRepo(db *sql.DB, id int64) (Repo, error) {
	r, err := scanRepo(db.QueryRow(`SELECT `+repoCols+` FROM repos WHERE id=?`, id))
	if err != nil {
		return Repo{}, fmt.Errorf("repo id %d: %w", id, err)
	}
	return r, nil
}

// GetRepoByName returns the repo or a wrapped sql.ErrNoRows.
func GetRepoByName(db *sql.DB, name string) (Repo, error) {
	r, err := scanRepo(db.QueryRow(`SELECT `+repoCols+` FROM repos WHERE name=?`, name))
	if err != nil {
		return Repo{}, fmt.Errorf("repo %q: %w", name, err)
	}
	return r, nil
}

// RemoveRepo deletes a repo; it fails while tasks reference it.
func RemoveRepo(db *sql.DB, name string) error {
	res, err := db.Exec(`DELETE FROM repos WHERE name=?`, name)
	if err != nil {
		return fmt.Errorf("remove repo %q: %w", name, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("repo %q: %w", name, sql.ErrNoRows)
	}
	return nil
}
