from __future__ import annotations

import io
import json
from pathlib import Path
import sys
import unittest
import urllib.error
from unittest import mock


SCRIPT_DIR = Path(__file__).resolve().parent
sys.path.insert(0, str(SCRIPT_DIR))

from surau_inference import (  # noqa: E402
    InferenceClient,
    InferenceError,
    budget_exceeded,
)


class SurauInferenceClientTest(unittest.TestCase):
    def test_invoke_sends_task_session_and_frozen_policy_hash(self) -> None:
        client = InferenceClient("http://surau.test", "service-token", 30)
        response = mock.MagicMock()
        response.__enter__.return_value = response
        response.read.return_value = b'{"output":"{}"}'
        with mock.patch("urllib.request.urlopen", return_value=response) as urlopen:
            result = client.invoke(
                "langextract-mentions",
                "rendered prompt",
                session_id="session-id",
                cache_vary={"langextract_task": "mentions"},
                variables={"policy_hash": "a" * 64},
            )

        request = urlopen.call_args.args[0]
        payload = json.loads(request.data.decode())
        self.assertEqual(result["output"], "{}")
        self.assertEqual(payload["task_key"], "langextract-mentions")
        self.assertEqual(payload["session_id"], "session-id")
        self.assertEqual(payload["variables"]["user"], "rendered prompt")
        self.assertEqual(payload["variables"]["policy_hash"], "a" * 64)
        self.assertEqual(request.headers["X-internal-token"], "service-token")

    def test_budget_error_survives_wrapper_chain(self) -> None:
        cap = InferenceError(
            "inference budget exceeded",
            code="inference_budget_exceeded",
            retry_after=120,
        )
        wrapper = RuntimeError("wrapped")
        wrapper.__cause__ = cap
        self.assertIs(budget_exceeded(wrapper), cap)

    def test_http_budget_error_is_stable_and_resumable(self) -> None:
        client = InferenceClient("http://surau.test", "service-token", 30)
        payload = io.BytesIO(
            b'{"code":"inference_budget_exceeded","message":"budget","retry_after":45}'
        )
        error = urllib.error.HTTPError(
            "http://surau.test/internal/inference/invoke",
            503,
            "Service Unavailable",
            {},
            payload,
        )
        with mock.patch("urllib.request.urlopen", side_effect=error):
            with self.assertRaises(InferenceError) as raised:
                client.invoke("reader-summary", "source")
        self.assertEqual(raised.exception.code, "inference_budget_exceeded")
        self.assertEqual(raised.exception.retry_after, 45)


if __name__ == "__main__":
    unittest.main()
