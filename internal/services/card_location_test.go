package services

import "testing"

func TestNormalizeCardLocationSplitsAddressWhenFieldsAreMissing(t *testing.T) {
	info := CardInfo{Address: "江苏省 淮安"}

	normalizeCardLocation(&info)

	if info.Province != "江苏省" || info.City != "淮安" {
		t.Fatalf("unexpected location: province=%q city=%q", info.Province, info.City)
	}
}

func TestNormalizeCardLocationKeepsExplicitFields(t *testing.T) {
	info := CardInfo{
		Province: "河南省",
		City:     "焦作",
		Address:  "江苏省 淮安",
	}

	normalizeCardLocation(&info)

	if info.Province != "河南省" || info.City != "焦作" {
		t.Fatalf("explicit location was overwritten: province=%q city=%q", info.Province, info.City)
	}
}

func TestNormalizeCardLocationLeavesUnstructuredAddressEmpty(t *testing.T) {
	info := CardInfo{Address: "华东地区"}

	normalizeCardLocation(&info)

	if info.Province != "" || info.City != "" {
		t.Fatalf("unstructured address should not be guessed: province=%q city=%q", info.Province, info.City)
	}
}
