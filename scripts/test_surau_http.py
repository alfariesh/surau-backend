from __future__ import annotations

import os
import unittest
from unittest import mock

from scripts.surau_http import surau_headers


class SurauHTTPIdentityTest(unittest.TestCase):
    def test_token_is_attached_only_to_exact_surau_origin(self) -> None:
        with mock.patch.dict(os.environ, {"SURAU_ENRICHMENT_SERVICE_TOKEN": "surau-test-token"}, clear=False):
            headers = surau_headers(
                "https://dev-api.surau.org",
                "https://dev-api.surau.org/v1/books/1",
                {"Accept": "application/json"},
            )
            self.assertEqual(headers["X-Internal-Token"], "surau-test-token")

            with self.assertRaisesRegex(ValueError, "different origin"):
                surau_headers(
                    "https://dev-api.surau.org",
                    "https://api.deepseek.com/chat/completions",
                    {"Authorization": "Bearer provider-key"},
                )

    def test_provider_headers_never_gain_surau_token(self) -> None:
        provider_headers = {"Authorization": "Bearer provider-key"}
        with mock.patch.dict(os.environ, {"SURAU_ENRICHMENT_SERVICE_TOKEN": "surau-test-token"}, clear=False):
            self.assertNotIn("X-Internal-Token", provider_headers)


if __name__ == "__main__":
    unittest.main()
