from __future__ import annotations


def normalize_target(target: str) -> str:
    return target.strip().lstrip("@")


def assert_allowed_target(target: str, allowed_groups: set[str], bot_username: str) -> None:
    normalized = normalize_target(target)
    allowed = {normalize_target(item) for item in allowed_groups}
    bot = normalize_target(bot_username)
    if normalized == bot or normalized in allowed:
        return
    raise ValueError(f"target not allowed: {target}")


def append_run_id(text: str, run_id: str) -> str:
    marker = f"#E2E-{run_id}"
    if marker in text:
        return text
    return f"{text.rstrip()} {marker}"

