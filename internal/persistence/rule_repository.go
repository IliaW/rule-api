package persistence

import (
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"sync"

	"github.com/IliaW/rule-api/internal/model"
	"github.com/IliaW/rule-api/util"
)

//go:generate go run github.com/vektra/mockery/v2@v2.53.0 --name RuleStorage
type RuleStorage interface {
	GetByUrl(string) (*model.Rule, error)
	GetById(string) (*model.Rule, error)
	Save(*model.Rule) (int64, error)
	Update(*model.Rule) (*model.Rule, error)
	Delete(string) error
}

type RuleRepository struct {
	db *sql.DB
	mu sync.Mutex
}

func NewRuleRepository(db *sql.DB) *RuleRepository {
	return &RuleRepository{
		db: db,
	}
}

func (r *RuleRepository) GetByUrl(url string) (*model.Rule, error) {
	domain, err := util.GetDomain(url)
	if err != nil {
		return nil, errors.New(fmt.Sprintf("failed to parse url. %s", err.Error()))
	}
	var rule model.Rule
	row := r.db.QueryRow(`SELECT id, domain, robots_txt, created_at, updated_at 
									FROM web_crawler.custom_rule WHERE domain = $1`, domain)
	err = row.Scan(&rule.ID, &rule.Domain, &rule.RobotsTxt, &rule.CreatedAt, &rule.UpdatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, errors.New(fmt.Sprintf("rule with domain '%s' not found", domain))
		}
		slog.Debug("failed to get rule from database.", slog.String("err", err.Error()))
		return nil, err
	}
	slog.Debug("rule fetched from db.")

	return &rule, nil
}

func (r *RuleRepository) GetById(id string) (*model.Rule, error) {
	var rule model.Rule
	row := r.db.QueryRow(`SELECT id, domain, robots_txt, created_at, updated_at 
									FROM web_crawler.custom_rule WHERE id = $1`,
		id)
	err := row.Scan(&rule.ID, &rule.Domain, &rule.RobotsTxt, &rule.CreatedAt, &rule.UpdatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, errors.New(fmt.Sprintf("rule with id '%s' not found", id))
		}
		slog.Debug("failed to get rule from database.", slog.String("err", err.Error()))
		return nil, err
	}
	slog.Debug("rule fetched from db.")

	return &rule, nil
}

func (r *RuleRepository) Save(rule *model.Rule) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var id int64
	err := r.db.QueryRow("INSERT INTO web_crawler.custom_rule (domain, robots_txt) VALUES ($1, $2) RETURNING id",
		rule.Domain, rule.RobotsTxt).Scan(&id)
	if err != nil {
		return 0, err
	}
	slog.Debug("rule saved to db.")

	return id, nil
}

func (r *RuleRepository) Update(rule *model.Rule) (*model.Rule, error) {
	_, err := r.db.Exec("UPDATE web_crawler.custom_rule SET domain = $1, robots_txt = $2 WHERE id = $3",
		rule.Domain, rule.RobotsTxt, rule.ID)
	if err != nil {
		return nil, err
	}
	slog.Debug("rule updated in db.")

	return r.GetById(strconv.Itoa(rule.ID))
}

func (r *RuleRepository) Delete(ruleId string) error {
	_, err := r.db.Exec("DELETE FROM web_crawler.custom_rule WHERE id = $1", ruleId)
	if err != nil {
		return err
	}
	slog.Debug("rule deleted from db.")

	return nil
}
