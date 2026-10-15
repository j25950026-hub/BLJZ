from __future__ import annotations

from dataclasses import dataclass, field
from typing import Any


@dataclass(frozen=True)
class Config:
    api_id: int
    api_hash: str
    bot_username: str
    test_groups: set[str]
    max_concurrency: int = 3
    min_delay_sec: int = 10
    max_delay_sec: int = 30
    report_dir: str = "reports"
    session_dir: str = "sessions"


@dataclass(frozen=True)
class Account:
    name: str
    role: str
    session: str


@dataclass(frozen=True)
class StepDefaults:
    timeout_sec: int = 20
    append_run_id: bool = True


@dataclass(frozen=True)
class Step:
    name: str
    account: str
    send: str
    expect_any: list[str] = field(default_factory=list)
    expect_none: list[str] = field(default_factory=list)
    timeout_sec: int | None = None
    append_run_id: bool | None = None


@dataclass(frozen=True)
class Suite:
    name: str
    target: str
    defaults: StepDefaults
    steps: list[Step]


@dataclass(frozen=True)
class StepResult:
    run_id: str
    suite: str
    case: str
    account: str
    target: str
    send: str
    expect: list[str]
    actual: str
    status: str
    latency_ms: int
    error_type: str = ""

    def to_json_dict(self) -> dict[str, Any]:
        return {
            "run_id": self.run_id,
            "suite": self.suite,
            "case": self.case,
            "account": self.account,
            "target": self.target,
            "send": self.send,
            "expect": self.expect,
            "actual": self.actual,
            "status": self.status,
            "latency_ms": self.latency_ms,
            "error_type": self.error_type,
        }

