package db

import (
	"encoding/json"
	"fmt"
	"strings"
)

func (d *Database) SaveAdminSavedView(ownerUsername, name string, filters map[string]string) error {
	ownerUsername = strings.TrimSpace(ownerUsername)
	name = strings.TrimSpace(name)
	if filters == nil {
		filters = map[string]string{}
	}
	cleanFilters := make(map[string]string, len(filters))
	for key, value := range filters {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key != "" && value != "" {
			cleanFilters[key] = value
		}
	}
	rawFilters, err := json.Marshal(cleanFilters)
	if err != nil {
		return err
	}
	_, err = d.raw.Exec(`
INSERT INTO admin_saved_views (owner_username, name, filters_json, created_at, updated_at)
VALUES (?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
ON CONFLICT(owner_username, name) DO UPDATE SET
	filters_json = excluded.filters_json,
	updated_at = CURRENT_TIMESTAMP`,
		ownerUsername,
		name,
		string(rawFilters),
	)
	return err
}

func (d *Database) ListAdminSavedViews(ownerUsername string) ([]AdminSavedView, error) {
	ownerUsername = strings.TrimSpace(ownerUsername)
	rows, err := d.raw.Query(`
SELECT owner_username, name, filters_json, updated_at
FROM admin_saved_views
WHERE owner_username = ?
ORDER BY updated_at DESC, name ASC`, ownerUsername)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []AdminSavedView{}
	for rows.Next() {
		var (
			item       AdminSavedView
			rawFilter  string
			filterData map[string]string
		)
		if err := rows.Scan(&item.OwnerUsername, &item.Name, &rawFilter, &item.UpdatedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(rawFilter), &filterData); err != nil || filterData == nil {
			filterData = map[string]string{}
		}
		item.Filters = filterData
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (d *Database) SaveAdminGroupDictionaryItem(kind, name, description string, sortOrder int) error {
	if d == nil || d.raw == nil {
		return fmt.Errorf("database is not configured")
	}
	kind, err := normalizeAdminGroupDictionaryKind(kind)
	if err != nil {
		return err
	}
	name = strings.TrimSpace(name)
	description = strings.TrimSpace(description)
	if name == "" {
		return fmt.Errorf("字典名称不能为空")
	}
	_, err = d.raw.Exec(`
INSERT INTO admin_group_dictionary_items (kind, name, description, sort_order, created_at, updated_at)
VALUES (?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
ON CONFLICT(kind, name) DO UPDATE SET
	description = excluded.description,
	sort_order = excluded.sort_order,
	updated_at = CURRENT_TIMESTAMP`,
		kind,
		name,
		description,
		sortOrder,
	)
	return err
}

func (d *Database) ListAdminGroupDictionaryItems(kind string) ([]AdminGroupDictionaryItem, error) {
	if d == nil || d.raw == nil {
		return nil, fmt.Errorf("database is not configured")
	}
	kind, err := normalizeAdminGroupDictionaryKind(kind)
	if err != nil {
		return nil, err
	}
	rows, err := d.raw.Query(`
SELECT kind, name, description, sort_order, updated_at
FROM admin_group_dictionary_items
WHERE kind = ?
ORDER BY sort_order ASC, name ASC`, kind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []AdminGroupDictionaryItem{}
	for rows.Next() {
		var item AdminGroupDictionaryItem
		if err := rows.Scan(&item.Kind, &item.Name, &item.Description, &item.SortOrder, &item.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (d *Database) DeleteAdminGroupDictionaryItem(kind, name string) error {
	if d == nil || d.raw == nil {
		return fmt.Errorf("database is not configured")
	}
	kind, err := normalizeAdminGroupDictionaryKind(kind)
	if err != nil {
		return err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("字典名称不能为空")
	}
	_, err = d.raw.Exec(`DELETE FROM admin_group_dictionary_items WHERE kind = ? AND name = ?`, kind, name)
	return err
}

func normalizeAdminGroupDictionaryKind(kind string) (string, error) {
	kind = strings.TrimSpace(kind)
	switch kind {
	case "group_type", "group_tag":
		return kind, nil
	default:
		return "", fmt.Errorf("不支持的字典类型")
	}
}
