from ledgerbot_e2e.report import mask_sensitive_text, summarize_results


def test_mask_sensitive_text_hides_tokens_and_sessions():
    text = "BOT_TOKEN=123456789:abcdefghijklmnopqrstuvwxyz sessions/user01.session"

    masked = mask_sensitive_text(text)

    assert "123456789:abcdefghijklmnopqrstuvwxyz" not in masked
    assert "sessions/user01.session" not in masked
    assert "[bot-token]" in masked
    assert "[session-path]" in masked


def test_summarize_results_groups_failures():
    rows = [
        {"suite": "ledger_core", "status": "pass", "error_type": ""},
        {"suite": "ledger_core", "status": "fail", "error_type": "timeout"},
        {"suite": "permission_matrix", "status": "fail", "error_type": "timeout"},
    ]

    summary = summarize_results(rows)

    assert summary["total"] == 3
    assert summary["passed"] == 1
    assert summary["failed"] == 2
    assert summary["errors"]["timeout"] == 2

