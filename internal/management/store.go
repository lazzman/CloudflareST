package management

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	_ "modernc.org/sqlite"
)

type querier interface {
	Exec(string, ...any) (sql.Result, error)
	Query(string, ...any) (*sql.Rows, error)
	QueryRow(string, ...any) *sql.Row
}

func put(query querier, kind, id string, value any) error {
	if kind == "runs" {
		if run, ok := value.(Run); ok {
			return putRun(query, id, run)
		}
	}
	return putRaw(query, kind, id, value)
}

func putRaw(query querier, kind, id string, value any) error {
	body, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = query.Exec("INSERT INTO objects(kind,id,body) VALUES(?,?,?) ON CONFLICT(kind,id) DO UPDATE SET body=excluded.body", kind, id, body)
	return err
}

func get[Value any](query querier, kind, id string) (Value, error) {
	value, err := getRaw[Value](query, kind, id)
	if err == nil && kind == "runs" {
		if run, ok := any(&value).(*Run); ok {
			run.Result.Candidates, err = loadCandidates(query, id)
		}
	}
	return value, err
}

func getRaw[Value any](query querier, kind, id string) (Value, error) {
	var value Value
	var body []byte
	err := query.QueryRow("SELECT body FROM objects WHERE kind=? AND id=?", kind, id).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		return value, errNotFound
	}
	if err != nil {
		return value, err
	}
	err = json.Unmarshal(body, &value)
	return value, err
}

func all[Value any](query querier, kind string) ([]Value, error) {
	rows, err := query.Query("SELECT body FROM objects WHERE kind=? ORDER BY id", kind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := make([]Value, 0)
	for rows.Next() {
		var body []byte
		if err := rows.Scan(&body); err != nil {
			return nil, err
		}
		var value Value
		if err := json.Unmarshal(body, &value); err != nil {
			return nil, fmt.Errorf("读取持久化对象失败: %w", err)
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func remove(query querier, kind, id string) error {
	if kind == "runs" {
		return runTransaction(query, func(transaction querier) error {
			if _, err := transaction.Exec("DELETE FROM run_candidates WHERE run_id=?", id); err != nil {
				return err
			}
			_, err := transaction.Exec("DELETE FROM objects WHERE kind=? AND id=?", kind, id)
			return err
		})
	}
	_, err := query.Exec("DELETE FROM objects WHERE kind=? AND id=?", kind, id)
	return err
}
