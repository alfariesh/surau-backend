"""Select the active, versioned search-key normalization profile."""

from .search_key_v1 import (
    PROFILE_NAME,
    PROFILE_VERSION,
    normalize_search_key_v1,
)

normalized_key = normalize_search_key_v1

__all__ = ["PROFILE_NAME", "PROFILE_VERSION", "normalized_key"]
