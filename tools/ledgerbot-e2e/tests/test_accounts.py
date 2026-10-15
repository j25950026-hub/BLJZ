from pathlib import Path

from ledgerbot_e2e.accounts import load_accounts
from ledgerbot_e2e.phones import load_phones


def test_load_accounts_rejects_duplicate_names(tmp_path: Path):
    path = tmp_path / "accounts.yml"
    path.write_text(
        """
accounts:
  - name: user01
    role: owner
    session: sessions/user01.session
  - name: user01
    role: member
    session: sessions/user01b.session
""",
        encoding="utf-8",
    )

    try:
        load_accounts(path)
    except ValueError as exc:
        assert "duplicate" in str(exc)
    else:
        raise AssertionError("expected ValueError")


def test_load_accounts_parses_session_paths(tmp_path: Path):
    path = tmp_path / "accounts.yml"
    path.write_text(
        """
accounts:
  - name: user01
    role: owner
    session: sessions/user01.session
""",
        encoding="utf-8",
    )

    accounts = load_accounts(path)

    assert accounts["user01"].role == "owner"
    assert accounts["user01"].session == "sessions/user01.session"


def test_load_phones_maps_account_to_phone(tmp_path: Path):
    path = tmp_path / "phones.local.yml"
    path.write_text(
        """
phones:
  - account: user01
    phone: "+10000000001"
  - account: user02
    phone: "+10000000002"
""",
        encoding="utf-8",
    )

    phones = load_phones(path)

    assert phones["user01"] == "+10000000001"
    assert phones["user02"] == "+10000000002"


def test_load_phones_missing_file_returns_empty(tmp_path: Path):
    phones = load_phones(tmp_path / "missing.yml")

    assert phones == {}
