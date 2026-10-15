from ledgerbot_e2e.guards import assert_allowed_target, append_run_id


def test_allows_configured_group():
    assert_allowed_target("e2e-main", {"e2e-main"}, "BL66888_bot")


def test_allows_configured_bot_private_target():
    assert_allowed_target("BL66888_bot", {"e2e-main"}, "BL66888_bot")


def test_blocks_unknown_target():
    try:
        assert_allowed_target("random-user", {"e2e-main"}, "BL66888_bot")
    except ValueError as exc:
        assert "not allowed" in str(exc)
    else:
        raise AssertionError("expected ValueError")


def test_append_run_id_is_idempotent():
    text = append_run_id("开始记账 #E2E-20260704-001", "20260704-001")

    assert text == "开始记账 #E2E-20260704-001"


def test_append_run_id_adds_marker():
    text = append_run_id("+100#E2E-deposit", "20260704-001")

    assert text == "+100#E2E-deposit #E2E-20260704-001"

