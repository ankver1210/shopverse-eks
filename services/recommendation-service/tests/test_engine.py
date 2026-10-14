import pytest

from app.engine import ProductNotFound, recommend

CATALOG = [
    {"id": 1, "category": "electronics", "price_cents": 10000},
    {"id": 2, "category": "electronics", "price_cents": 9000},
    {"id": 3, "category": "electronics", "price_cents": 50000},
    {"id": 4, "category": "home", "price_cents": 3000},
    {"id": 5, "category": "books", "price_cents": 2000},
]


def test_same_category_ranked_by_price_similarity_first():
    picks = recommend(CATALOG, 1, limit=3)
    assert [p["id"] for p in picks] == [2, 3, 4]


def test_never_recommends_the_product_itself():
    assert all(p["id"] != 1 for p in recommend(CATALOG, 1, limit=10))


def test_unknown_product():
    with pytest.raises(ProductNotFound):
        recommend(CATALOG, 999)
