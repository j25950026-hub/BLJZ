import asyncio

from ledgerbot_e2e.models import Account, Config
from ledgerbot_e2e.telegram_client import TelegramClientAdapter


class _AsyncMessages:
    def __init__(self, messages):
        self._messages = list(messages)

    def __aiter__(self):
        self._index = 0
        return self

    async def __anext__(self):
        if self._index >= len(self._messages):
            raise StopAsyncIteration
        item = self._messages[self._index]
        self._index += 1
        return item


class _Message:
    def __init__(self, message_id: int):
        self.id = message_id


class _AuthorizedClient:
    def __init__(self):
        self.connected = False
        self.disconnected = False
        self.sent = []

    async def connect(self):
        self.connected = True

    async def is_user_authorized(self):
        return True

    async def start(self):
        raise AssertionError("send_message must not prompt for interactive login")

    async def disconnect(self):
        self.disconnected = True

    async def get_entity(self, target):
        return target

    def iter_messages(self, entity, limit):
        return _AsyncMessages([_Message(1)])

    async def send_message(self, entity, text):
        self.sent.append((entity, text))


def test_send_message_uses_existing_authorized_session_without_start():
    cfg = Config(
        api_id=1,
        api_hash="hash",
        bot_username="BL66888_bot",
        test_groups={"e2e-main"},
    )
    account = Account(name="user01", role="member", session="sessions/user01.session")
    adapter = TelegramClientAdapter(cfg, {"user01": account})
    client = _AuthorizedClient()
    adapter._new_client = lambda _: client

    result = asyncio.run(adapter.send_message("user01", "e2e-main", "ping", timeout_sec=0))

    assert result == ""
    assert client.connected is True
    assert client.disconnected is True
    assert client.sent == [("e2e-main", "ping")]


def test_send_message_resolves_numeric_chat_id_as_integer():
    cfg = Config(
        api_id=1,
        api_hash="hash",
        bot_username="BL66888_bot",
        test_groups={"-1004359368796"},
    )
    account = Account(name="user01", role="member", session="sessions/user01.session")
    adapter = TelegramClientAdapter(cfg, {"user01": account})
    client = _AuthorizedClient()
    adapter._new_client = lambda _: client

    asyncio.run(adapter.send_message("user01", "-1004359368796", "ping", timeout_sec=0))

    assert client.sent == [(-1004359368796, "ping")]
