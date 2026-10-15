from __future__ import annotations

import argparse
import asyncio
import random
import time
from datetime import datetime
from pathlib import Path

from .accounts import load_accounts
from .config import load_config
from .guards import append_run_id, assert_allowed_target
from .models import Step, StepResult, Suite
from .report import write_jsonl
from .suite_loader import load_suite
from .telegram_client import TelegramClientAdapter


def assert_response(step: Step, actual: str) -> tuple[str, str]:
    if step.expect_any and not any(item in actual for item in step.expect_any):
        return "fail", "assertion_failed"
    if step.expect_none and any(item in actual for item in step.expect_none):
        return "fail", "assertion_failed"
    return "pass", ""


async def run_suite(
    suite: Suite,
    adapter: TelegramClientAdapter,
    run_id: str,
    report_path: Path,
    min_delay_sec: int,
    max_delay_sec: int,
) -> list[StepResult]:
    results: list[StepResult] = []
    for step in suite.steps:
        timeout_sec = step.timeout_sec or suite.defaults.timeout_sec
        should_append = suite.defaults.append_run_id if step.append_run_id is None else step.append_run_id
        send_text = append_run_id(step.send, run_id) if should_append else step.send
        started = time.monotonic()
        actual = ""
        status = "fail"
        error_type = "unknown"
        try:
            actual = await adapter.send_message(step.account, suite.target, send_text, timeout_sec)
            status, error_type = assert_response(step, actual)
        except Exception as exc:  # noqa: BLE001 - classify runtime failures for JSONL reports.
            actual = str(exc)
            name = exc.__class__.__name__.lower()
            error_type = "flood_wait" if "flood" in name else "telegram_error"
        latency_ms = int((time.monotonic() - started) * 1000)
        result = StepResult(
            run_id=run_id,
            suite=suite.name,
            case=step.name,
            account=step.account,
            target=suite.target,
            send=send_text,
            expect=step.expect_any or step.expect_none,
            actual=actual,
            status=status,
            latency_ms=latency_ms,
            error_type=error_type,
        )
        results.append(result)
        write_jsonl(report_path, [result])
        if max_delay_sec > 0:
            await asyncio.sleep(random.randint(min_delay_sec, max_delay_sec))
    return results


def validate_dry_run(suite: Suite, accounts_path: str, allowed_groups: set[str], bot_username: str) -> None:
    accounts = load_accounts(accounts_path)
    assert_allowed_target(suite.target, allowed_groups, bot_username)
    missing = [step.account for step in suite.steps if step.account not in accounts]
    if missing:
        raise ValueError(f"suite references unknown accounts: {', '.join(sorted(set(missing)))}")


def main() -> int:
    parser = argparse.ArgumentParser(description="Run LedgerBot E2E suites.")
    parser.add_argument("--suite", required=True)
    parser.add_argument("--config", default=".env")
    parser.add_argument("--accounts", default="accounts.yml")
    parser.add_argument("--dry-run", action="store_true")
    parser.add_argument("--run-id")
    parser.add_argument("--max-concurrency", type=int)
    args = parser.parse_args()

    cfg = load_config(args.config)
    suite = load_suite(args.suite)
    validate_dry_run(suite, args.accounts, cfg.test_groups, cfg.bot_username)

    if args.dry_run:
        print(f"dry-run ok: suite={suite.name} target={suite.target} steps={len(suite.steps)}")
        return 0

    accounts = load_accounts(args.accounts)
    adapter = TelegramClientAdapter(cfg, accounts)
    run_id = args.run_id or datetime.utcnow().strftime("%Y%m%d-%H%M%S")
    report_path = Path(cfg.report_dir) / f"{run_id}.jsonl"
    asyncio.run(
        run_suite(
            suite=suite,
            adapter=adapter,
            run_id=run_id,
            report_path=report_path,
            min_delay_sec=cfg.min_delay_sec,
            max_delay_sec=cfg.max_delay_sec,
        )
    )
    print(f"report written: {report_path}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

