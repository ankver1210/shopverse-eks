import os

os.environ.setdefault("JWT_SECRET", "test-secret-that-is-at-least-32-bytes-long")
os.environ.setdefault("BCRYPT_ROUNDS", "4")

import jwt  # noqa: E402
import pytest  # noqa: E402
from pydantic import ValidationError  # noqa: E402

from app.main import JWT_ISSUER, RegisterRequest, issue_token  # noqa: E402


def test_token_contains_identity_claims():
    token = issue_token({"id": 42, "email": "ada@example.com", "name": "Ada"})
    claims = jwt.decode(token, "test-secret-that-is-at-least-32-bytes-long", algorithms=["HS256"], issuer=JWT_ISSUER)
    assert claims["sub"] == "42"
    assert claims["email"] == "ada@example.com"
    assert claims["exp"] > claims["iat"]


def test_register_rejects_short_password():
    with pytest.raises(ValidationError):
        RegisterRequest(email="ada@example.com", password="short", name="Ada")


def test_register_rejects_invalid_email():
    with pytest.raises(ValidationError):
        RegisterRequest(email="not-an-email", password="long-enough-pw", name="Ada")
