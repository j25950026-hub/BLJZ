from __future__ import annotations

import asyncio
from pathlib import Path

from .models import Account, Config


class TelegramDependencyError(RuntimeError):
    pass


class TelegramClientAdapter:
    def __init__(self, cfg: Config, accounts: dict[str, Account]):
        self.cfg = cfg
        self.accounts = accounts

    async def login_account(self, account_name: str, phone: str | None = None, skip_authorized: bool = True) -> str:
        account = self._account(account_name)
        client = self._new_client(account)
        await client.connect()
        try:
            if skip_authorized and await client.is_user_authorized():
                me = await client.get_me()
                return f"skip authorized {account.name}: id={getattr(me, 'id', '')} username={getattr(me, 'username', '')}"
            await client.start(phone=phone)
            me = await client.get_me()
            return f"logged in {account.name}: id={getattr(me, 'id', '')} username={getattr(me, 'username', '')}"
        finally:
            await client.disconnect()

    async def session_file_exists(self, account_name: str) -> bool:
        account = self._account(account_name)
        return Path(account.session).exists()

    async def send_message(self, account_name: str, target: str, text: str, timeout_sec: int) -> str:
        account = self._account(account_name)
        client = self._new_client(account)
        await client.connect()
        try:
            if not await client.is_user_authorized():
                raise RuntimeError(f"session is not authorized for account {account.name}; run login_sessions first")
            entity = await client.get_entity(_resolve_target(target))
            before_id = 0
            async for message in client.iter_messages(entity, limit=1):
                before_id = int(message.id)
                break
            await client.send_message(entity, text)
            deadline = asyncio.get_event_loop().time() + timeout_sec
            while asyncio.get_event_loop().time() < deadline:
                await asyncio.sleep(1)
                async for message in client.iter_messages(entity, limit=10):
                    if int(message.id) <= before_id:
                        break
                    body = str(getattr(message, "message", "") or "")
                    sender = await message.get_sender()
                    username = str(getattr(sender, "username", "") or "").lstrip("@")
                    if username == self.cfg.bot_username or self.cfg.bot_username.lower() in username.lower():
                        return body
            return ""
        finally:
            await client.disconnect()

    def _account(self, account_name: str) -> Account:
        if account_name not in self.accounts:
            raise ValueError(f"unknown account: {account_name}")
        return self.accounts[account_name]

    def _new_client(self, account: Account):
        try:
            from telethon import TelegramClient
        except ImportError as exc:
            raise TelegramDependencyError(
                "telethon is not installed. Run: pip install -r requirements.txt"
            ) from exc
        Path(account.session).parent.mkdir(parents=True, exist_ok=True)
        return TelegramClient(account.session, self.cfg.api_id, self.cfg.api_hash)


def _resolve_target(target: str) -> str | int:
    normalized = str(target).strip()
    if normalized and normalized.lstrip("-").isdigit():
        return int(normalized)
    return normalized
