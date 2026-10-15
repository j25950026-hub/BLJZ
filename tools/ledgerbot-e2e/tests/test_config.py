from ledgerbot_e2e.config import load_config_from_mapping


def test_load_config_rejects_missing_bot_username():
    raw = {"API_ID": "1", "API_HASH": "hash", "TEST_GROUPS": "e2e-main"}

    try:
        load_config_from_mapping(raw)
    except ValueError as exc:
        assert "BOT_USERNAME" in str(exc)
    else:
        raise AssertionError("expected ValueError")


def test_load_config_parses_limits_and_groups():
    cfg = load_config_from_mapping(
        {
            "API_ID": "1",
            "API_HASH": "hash",
            "BOT_USERNAME": "BL66888_bot",
            "TEST_GROUPS": "e2e-main,e2e-secondary",
            "MAX_CONCURRENCY": "3",
            "MIN_DELAY_SEC": "10",
            "MAX_DELAY_SEC": "30",
        }
    )

    assert cfg.api_id == 1
    assert cfg.bot_username == "BL66888_bot"
    assert cfg.test_groups == {"e2e-main", "e2e-secondary"}
    assert cfg.max_concurrency == 3
    assert cfg.min_delay_sec == 10
    assert cfg.max_delay_sec == 30


def test_load_config_rejects_delay_range_inversion():
    raw = {
        "API_ID": "1",
        "API_HASH": "hash",
        "BOT_USERNAME": "BL66888_bot",
        "TEST_GROUPS": "e2e-main",
        "MIN_DELAY_SEC": "30",
        "MAX_DELAY_SEC": "10",
    }

    try:
        load_config_from_mapping(raw)
    except ValueError as exc:
        assert "MAX_DELAY_SEC" in str(exc)
    else:
        raise AssertionError("expected ValueError")

