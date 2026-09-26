package identity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode"

	"controlplane/internal/platform/id"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var roleCodePattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{1,31}$`)

type NewRole struct {
	Code        string   `json:"code"`
	Description string   `json:"description"`
	Permissions []string `json:"permissions"`
}

type RolePatch struct {
	Description *string   `json:"description,omitempty"`
	Permissions *[]string `json:"permissions,omitempty"`
}

type RoleAssignment struct {
	RoleCodes []string `json:"role_codes"`
}

type RoleInput struct {
	Code        string
	Description string
	Permissions []string
}

type RolePatchInput struct {
	Description *string
	Permissions *[]string
}

type Role struct {
	ID          string   `json:"id"`
	Code        string   `json:"code"`
	Description string   `json:"description"`
	System      bool     `json:"system"`
	Permissions []string `json:"permissions"`
	MemberCount int      `json:"member_count"`
}

type Permission struct {
	Code        string `json:"code"`
	Description string `json:"description"`
}

func NormalizeRole(input NewRole) (RoleInput, error) {
	code := strings.ToLower(strings.TrimSpace(input.Code))
	if !roleCodePattern.MatchString(code) || code == "admin" || code == "user" {
		return RoleInput{}, ErrInvalidInput
	}
	description := strings.TrimSpace(input.Description)
	if description == "" || len([]rune(description)) > 200 || roleHasControl(description) {
		return RoleInput{}, ErrInvalidInput
	}
	if input.Permissions == nil {
		return RoleInput{}, ErrInvalidInput
	}
	permissions, err := normalizePermissionCodes(input.Permissions)
	if err != nil {
		return RoleInput{}, err
	}
	return RoleInput{Code: code, Description: description, Permissions: permissions}, nil
}

func NormalizeRolePatch(input RolePatch) (RolePatchInput, error) {
	if input.Description == nil && input.Permissions == nil {
		return RolePatchInput{}, ErrInvalidInput
	}
	var description *string
	if input.Description != nil {
		value := strings.TrimSpace(*input.Description)
		if value == "" || len([]rune(value)) > 200 || roleHasControl(value) {
			return RolePatchInput{}, ErrInvalidInput
		}
		description = &value
	}
	var permissions *[]string
	if input.Permissions != nil {
		values, err := normalizePermissionCodes(*input.Permissions)
		if err != nil {
			return RolePatchInput{}, err
		}
		permissions = &values
	}
	return RolePatchInput{Description: description, Permissions: permissions}, nil
}

func NormalizeRoleAssignment(input RoleAssignment) (RoleAssignment, error) {
	if input.RoleCodes == nil {
		return RoleAssignment{}, ErrInvalidInput
	}
	if len(input.RoleCodes) > 32 {
		return RoleAssignment{}, ErrInvalidInput
	}
	seen := make(map[string]struct{}, len(input.RoleCodes))
	codes := make([]string, 0, len(input.RoleCodes))
	for _, raw := range input.RoleCodes {
		code := strings.ToLower(strings.TrimSpace(raw))
		if !roleCodePattern.MatchString(code) || code == "admin" || code == "user" {
			return RoleAssignment{}, ErrInvalidInput
		}
		if _, ok := seen[code]; ok {
			return RoleAssignment{}, ErrInvalidInput
		}
		seen[code] = struct{}{}
		codes = append(codes, code)
	}
	slices.Sort(codes)
	return RoleAssignment{RoleCodes: codes}, nil
}

func normalizePermissionCodes(input []string) ([]string, error) {
	if len(input) > 64 {
		return nil, ErrInvalidInput
	}
	seen := make(map[string]struct{}, len(input))
	permissions := make([]string, 0, len(input))
	for _, raw := range input {
		code := strings.TrimSpace(raw)
		if code == "" || len(code) > 100 || roleHasControl(code) {
			return nil, ErrInvalidInput
		}
		if code == "roles.write" {
			return nil, ErrInvalidInput
		}
		if _, ok := seen[code]; ok {
			return nil, ErrInvalidInput
		}
		seen[code] = struct{}{}
		permissions = append(permissions, code)
	}
	slices.Sort(permissions)
	return permissions, nil
}

func roleHasControl(value string) bool {
	for _, r := range value {
		if unicode.IsControl(r) {
			return true
		}
	}
	return false
}

var ErrRoleSystem = errors.New("system role cannot be changed")
var ErrUnknownPermission = errors.New("unknown permission")
var ErrRoleInUse = errors.New("role is assigned to users")

func roleSystem(code string) bool { return code == "admin" || code == "user" }

func validatePermissionCodes(ctx context.Context, tx pgx.Tx, codes []string) error {
	if len(codes) == 0 {
		return nil
	}
	if slices.Contains(codes, "roles.write") {
		return ErrInvalidInput
	}
	var count int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM permissions WHERE code = ANY($1::text[])`, codes).Scan(&count); err != nil {
		return fmt.Errorf("count permissions: %w", err)
	}
	if count != len(codes) {
		return ErrUnknownPermission
	}
	return nil
}

func roleAuditJSON(role Role) (string, error) {
	b, err := json.Marshal(role)
	return string(b), err
}

func scanRole(row pgx.Row) (Role, error) {
	var role Role
	if err := row.Scan(&role.ID, &role.Code, &role.Description, &role.System, &role.Permissions, &role.MemberCount); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Role{}, ErrNotFound
		}
		return Role{}, err
	}
	return role, nil
}

const roleSelect = `SELECT r.id::text,r.code,r.description,r.code IN ('admin','user'),
COALESCE(array_agg(DISTINCT rp.permission_code) FILTER (WHERE rp.permission_code IS NOT NULL),'{}'::text[]),
count(DISTINCT ur.user_id)
FROM roles r
LEFT JOIN role_permissions rp ON rp.role_code=r.code
LEFT JOIN user_roles ur ON ur.role_code=r.code`

func (r *PostgresRepository) ListRoles(ctx context.Context) ([]Role, error) {
	rows, err := r.pool.Query(ctx, roleSelect+` GROUP BY r.id,r.code,r.description ORDER BY r.code`)
	if err != nil {
		return nil, fmt.Errorf("list roles: %w", err)
	}
	defer rows.Close()
	roles := make([]Role, 0)
	for rows.Next() {
		role, err := scanRole(rows)
		if err != nil {
			return nil, fmt.Errorf("scan role: %w", err)
		}
		roles = append(roles, role)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate roles: %w", err)
	}
	return roles, nil
}

func (r *PostgresRepository) ListPermissions(ctx context.Context) ([]Permission, error) {
	rows, err := r.pool.Query(ctx, `SELECT code,description FROM permissions ORDER BY code`)
	if err != nil {
		return nil, fmt.Errorf("list permissions: %w", err)
	}
	defer rows.Close()
	permissions := make([]Permission, 0)
	for rows.Next() {
		var permission Permission
		if err := rows.Scan(&permission.Code, &permission.Description); err != nil {
			return nil, fmt.Errorf("scan permission: %w", err)
		}
		permissions = append(permissions, permission)
	}
	return permissions, rows.Err()
}

func (r *PostgresRepository) CreateRole(ctx context.Context, input RoleInput, actorID, requestID string) (Role, error) {
	roleID, err := id.NewV7()
	if err != nil {
		return Role{}, err
	}
	auditID, err := id.NewV7()
	if err != nil {
		return Role{}, err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Role{}, fmt.Errorf("begin role creation: %w", err)
	}
	defer tx.Rollback(ctx)
	if err := validatePermissionCodes(ctx, tx, input.Permissions); err != nil {
		return Role{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO roles(id,code,description) VALUES($1,$2,$3)`, roleID, input.Code, input.Description); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return Role{}, ErrAlreadyExists
		}
		return Role{}, fmt.Errorf("insert role: %w", err)
	}
	if len(input.Permissions) > 0 {
		if _, err := tx.Exec(ctx, `INSERT INTO role_permissions(role_code,permission_code) SELECT $1,unnest($2::text[])`, input.Code, input.Permissions); err != nil {
			return Role{}, fmt.Errorf("insert role permissions: %w", err)
		}
	}
	role, err := scanRole(tx.QueryRow(ctx, roleSelect+` WHERE r.code=$1 GROUP BY r.id,r.code,r.description`, input.Code))
	if err != nil {
		return Role{}, fmt.Errorf("load created role: %w", err)
	}
	after, err := roleAuditJSON(role)
	if err != nil {
		return Role{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_logs(id,actor_user_id,action,object_type,object_id,after_json,request_id) VALUES($1,$2,'create','role',$3,$4,$5)`, auditID, actorID, role.ID, after, requestID); err != nil {
		return Role{}, fmt.Errorf("audit role creation: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Role{}, fmt.Errorf("commit role creation: %w", err)
	}
	return role, nil
}

func (r *PostgresRepository) UpdateRole(ctx context.Context, code string, patch RolePatchInput, actorID, requestID string) (Role, error) {
	auditID, err := id.NewV7()
	if err != nil {
		return Role{}, err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Role{}, fmt.Errorf("begin role update: %w", err)
	}
	defer tx.Rollback(ctx)
	var lockedCode string
	if err := tx.QueryRow(ctx, `SELECT code FROM roles WHERE code=$1 FOR UPDATE`, code).Scan(&lockedCode); errors.Is(err, pgx.ErrNoRows) {
		return Role{}, ErrNotFound
	} else if err != nil {
		return Role{}, fmt.Errorf("lock role: %w", err)
	}
	before, err := scanRole(tx.QueryRow(ctx, roleSelect+` WHERE r.code=$1 GROUP BY r.id,r.code,r.description`, code))
	if err != nil {
		return Role{}, err
	}
	if before.System {
		return Role{}, ErrRoleSystem
	}
	if patch.Permissions != nil {
		if err := validatePermissionCodes(ctx, tx, *patch.Permissions); err != nil {
			return Role{}, err
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE roles SET description=COALESCE($2,description) WHERE code=$1`, code, patch.Description); err != nil {
		return Role{}, fmt.Errorf("update role: %w", err)
	}
	if patch.Permissions != nil {
		if _, err := tx.Exec(ctx, `DELETE FROM role_permissions WHERE role_code=$1`, code); err != nil {
			return Role{}, fmt.Errorf("clear role permissions: %w", err)
		}
		if len(*patch.Permissions) > 0 {
			if _, err := tx.Exec(ctx, `INSERT INTO role_permissions(role_code,permission_code) SELECT $1,unnest($2::text[])`, code, *patch.Permissions); err != nil {
				return Role{}, fmt.Errorf("replace role permissions: %w", err)
			}
		}
	}
	after, err := scanRole(tx.QueryRow(ctx, roleSelect+` WHERE r.code=$1 GROUP BY r.id,r.code,r.description`, code))
	if err != nil {
		return Role{}, fmt.Errorf("load updated role: %w", err)
	}
	beforeJSON, err := roleAuditJSON(before)
	if err != nil {
		return Role{}, err
	}
	afterJSON, err := roleAuditJSON(after)
	if err != nil {
		return Role{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_logs(id,actor_user_id,action,object_type,object_id,before_json,after_json,request_id) VALUES($1,$2,'update','role',$3,$4,$5,$6)`, auditID, actorID, before.ID, beforeJSON, afterJSON, requestID); err != nil {
		return Role{}, fmt.Errorf("audit role update: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Role{}, fmt.Errorf("commit role update: %w", err)
	}
	return after, nil
}

func (r *PostgresRepository) DeleteRole(ctx context.Context, code, actorID, requestID string) error {
	auditID, err := id.NewV7()
	if err != nil {
		return err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin role deletion: %w", err)
	}
	defer tx.Rollback(ctx)
	var lockedCode string
	if err := tx.QueryRow(ctx, `SELECT code FROM roles WHERE code=$1 FOR UPDATE`, code).Scan(&lockedCode); errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return fmt.Errorf("lock role: %w", err)
	}
	if roleSystem(code) {
		return ErrRoleSystem
	}
	before, err := scanRole(tx.QueryRow(ctx, roleSelect+` WHERE r.code=$1 GROUP BY r.id,r.code,r.description`, code))
	if err != nil {
		return err
	}
	if before.MemberCount > 0 {
		return ErrRoleInUse
	}
	if _, err := tx.Exec(ctx, `DELETE FROM roles WHERE code=$1`, code); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23503" {
			return ErrRoleInUse
		}
		return fmt.Errorf("delete role: %w", err)
	}
	beforeJSON, err := roleAuditJSON(before)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_logs(id,actor_user_id,action,object_type,object_id,before_json,request_id) VALUES($1,$2,'delete','role',$3,$4,$5)`, auditID, actorID, before.ID, beforeJSON, requestID); err != nil {
		return fmt.Errorf("audit role deletion: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit role deletion: %w", err)
	}
	return nil
}

func (r *PostgresRepository) SetUserRoles(ctx context.Context, userID string, assignment RoleAssignment, actorID, requestID string) (PublicUser, error) {
	assignment, err := NormalizeRoleAssignment(assignment)
	if err != nil {
		return PublicUser{}, err
	}
	auditID, err := id.NewV7()
	if err != nil {
		return PublicUser{}, err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return PublicUser{}, fmt.Errorf("begin user role assignment: %w", err)
	}
	defer tx.Rollback(ctx)
	var canonicalUserID string
	var administrator bool
	if err := tx.QueryRow(ctx, `SELECT id::text, EXISTS(SELECT 1 FROM user_roles WHERE user_id=$1 AND role_code='admin') FROM users WHERE id=$1 FOR UPDATE`, userID).Scan(&canonicalUserID, &administrator); errors.Is(err, pgx.ErrNoRows) {
		return PublicUser{}, ErrNotFound
	} else if err != nil {
		return PublicUser{}, fmt.Errorf("lock role user: %w", err)
	}
	if administrator || strings.EqualFold(canonicalUserID, actorID) {
		return PublicUser{}, ErrInvalidInput
	}
	if len(assignment.RoleCodes) > 0 {
		rows, err := tx.Query(ctx, `SELECT code FROM roles WHERE code=ANY($1::text[]) AND code NOT IN ('admin','user') ORDER BY code FOR UPDATE`, assignment.RoleCodes)
		if err != nil {
			return PublicUser{}, fmt.Errorf("lock assigned roles: %w", err)
		}
		count := 0
		for rows.Next() {
			count++
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return PublicUser{}, fmt.Errorf("iterate assigned roles: %w", err)
		}
		if count != len(assignment.RoleCodes) {
			return PublicUser{}, ErrNotFound
		}
	}
	before, err := loadUserByID(ctx, tx, userID)
	if err != nil {
		return PublicUser{}, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM user_roles WHERE user_id=$1 AND role_code<>'user'`, userID); err != nil {
		return PublicUser{}, fmt.Errorf("clear user custom roles: %w", err)
	}
	if len(assignment.RoleCodes) > 0 {
		if _, err := tx.Exec(ctx, `INSERT INTO user_roles(user_id,role_code) SELECT $1,unnest($2::text[])`, userID, assignment.RoleCodes); err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23503" {
				return PublicUser{}, ErrNotFound
			}
			return PublicUser{}, fmt.Errorf("assign user custom roles: %w", err)
		}
	}
	after, err := loadUserByID(ctx, tx, userID)
	if err != nil {
		return PublicUser{}, err
	}
	beforeJSON, err := json.Marshal(before.Public())
	if err != nil {
		return PublicUser{}, err
	}
	afterJSON, err := json.Marshal(after.Public())
	if err != nil {
		return PublicUser{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_logs(id,actor_user_id,action,object_type,object_id,before_json,after_json,request_id) VALUES($1,$2,'update','user',$3,$4,$5,$6)`, auditID, actorID, userID, string(beforeJSON), string(afterJSON), requestID); err != nil {
		return PublicUser{}, fmt.Errorf("audit user roles: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return PublicUser{}, fmt.Errorf("commit user role assignment: %w", err)
	}
	return after.Public(), nil
}
