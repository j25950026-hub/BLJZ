package db

import (
	"encoding/json"
	"fmt"
	"strings"
)

var adminTemplateLibraryKinds = map[string]struct{}{
	"welcome":          {},
	"auto_reply":       {},
	"closing_reminder": {},
	"broadcast":        {},
}

func (d *Database) SaveAdminTemplateLibrary(template AdminTemplateLibrary) (AdminTemplateLibrary, error) {
	if d == nil || d.raw == nil {
		return AdminTemplateLibrary{}, fmt.Errorf("database is not configured")
	}
	template.Kind = strings.ToLower(strings.TrimSpace(template.Kind))
	template.Name = strings.TrimSpace(template.Name)
	template.Description = strings.TrimSpace(template.Description)
	template.OwnerUsername = strings.TrimSpace(template.OwnerUsername)
	if _, ok := adminTemplateLibraryKinds[template.Kind]; !ok {
		return AdminTemplateLibrary{}, fmt.Errorf("不支持的模板类型")
	}
	if template.Name == "" {
		return AdminTemplateLibrary{}, fmt.Errorf("模板名称不能为空")
	}
	if template.ContentJSON == "" || !json.Valid([]byte(template.ContentJSON)) {
		return AdminTemplateLibrary{}, fmt.Errorf("模板内容必须是合法 JSON")
	}
	var nextVersion int
	if err := d.raw.QueryRow(`SELECT COALESCE(MAX(version), 0) + 1 FROM admin_template_library WHERE kind = ? AND name = ?`, template.Kind, template.Name).Scan(&nextVersion); err != nil {
		return AdminTemplateLibrary{}, err
	}
	result, err := d.raw.Exec(`
INSERT INTO admin_template_library (kind, name, description, version, content_json, owner_username)
VALUES (?, ?, ?, ?, ?, ?)`, template.Kind, template.Name, template.Description, nextVersion, template.ContentJSON, template.OwnerUsername)
	if err != nil {
		return AdminTemplateLibrary{}, err
	}
	template.ID, err = result.LastInsertId()
	if err != nil {
		return AdminTemplateLibrary{}, err
	}
	template.Version = nextVersion
	return template, nil
}

func (d *Database) ListAdminTemplateLibrary(kind string) ([]AdminTemplateLibrary, error) {
	if d == nil || d.raw == nil {
		return nil, fmt.Errorf("database is not configured")
	}
	kind = strings.ToLower(strings.TrimSpace(kind))
	query := `
SELECT id, kind, name, description, version, content_json, owner_username, COALESCE(created_at, '')
FROM admin_template_library`
	args := []any{}
	if kind != "" {
		if _, ok := adminTemplateLibraryKinds[kind]; !ok {
			return nil, fmt.Errorf("不支持的模板类型")
		}
		query += ` WHERE kind = ?`
		args = append(args, kind)
	}
	query += ` ORDER BY kind ASC, name ASC, version DESC, id DESC`
	rows, err := d.raw.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []AdminTemplateLibrary{}
	for rows.Next() {
		var item AdminTemplateLibrary
		if err := rows.Scan(&item.ID, &item.Kind, &item.Name, &item.Description, &item.Version, &item.ContentJSON, &item.OwnerUsername, &item.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

func (d *Database) GetAdminTemplateLibrary(id int64) (AdminTemplateLibrary, error) {
	if d == nil || d.raw == nil {
		return AdminTemplateLibrary{}, fmt.Errorf("database is not configured")
	}
	if id == 0 {
		return AdminTemplateLibrary{}, fmt.Errorf("template id is required")
	}
	var item AdminTemplateLibrary
	err := d.raw.QueryRow(`
SELECT id, kind, name, description, version, content_json, owner_username, COALESCE(created_at, '')
FROM admin_template_library
WHERE id = ?`, id).Scan(&item.ID, &item.Kind, &item.Name, &item.Description, &item.Version, &item.ContentJSON, &item.OwnerUsername, &item.CreatedAt)
	if err != nil {
		return AdminTemplateLibrary{}, err
	}
	return item, nil
}
