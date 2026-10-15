from pathlib import Path

from ledgerbot_e2e.suite_loader import load_suite


def test_load_suite_requires_expectation(tmp_path: Path):
    suite_path = tmp_path / "bad.yml"
    suite_path.write_text(
        """
suite: bad
target: e2e-main
steps:
  - name: missing_expectation
    account: user01
    send: "开始记账"
""",
        encoding="utf-8",
    )

    try:
        load_suite(suite_path)
    except ValueError as exc:
        assert "expect_any" in str(exc)
    else:
        raise AssertionError("expected ValueError")


def test_load_suite_parses_steps(tmp_path: Path):
    suite_path = tmp_path / "ledger_core.yml"
    suite_path.write_text(
        """
suite: ledger_core
target: e2e-main
defaults:
  timeout_sec: 20
  append_run_id: true
steps:
  - name: deposit
    account: user02
    send: "+100#E2E-deposit"
    expect_any: ["入款", "100"]
""",
        encoding="utf-8",
    )

    suite = load_suite(suite_path)

    assert suite.name == "ledger_core"
    assert suite.target == "e2e-main"
    assert suite.defaults.timeout_sec == 20
    assert suite.defaults.append_run_id is True
    assert suite.steps[0].name == "deposit"
    assert suite.steps[0].expect_any == ["入款", "100"]

