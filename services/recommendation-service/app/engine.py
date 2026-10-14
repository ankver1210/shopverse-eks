"""Recommendation logic (pure, unit tested).

Strategy: products from the same category ranked by price similarity, then the most
expensive items from other categories as "you may also like" filler. Simple, explainable,
and good enough to demonstrate a stateless read-heavy microservice with caching.
"""
from __future__ import annotations


class ProductNotFound(Exception):
    pass


def recommend(catalog: list[dict], product_id: int, limit: int = 4) -> list[dict]:
    target = next((p for p in catalog if p["id"] == product_id), None)
    if target is None:
        raise ProductNotFound(product_id)
    same_category = sorted(
        (p for p in catalog if p["category"] == target["category"] and p["id"] != product_id),
        key=lambda p: abs(p["price_cents"] - target["price_cents"]),
    )
    others = sorted(
        (p for p in catalog if p["category"] != target["category"]),
        key=lambda p: (-p["price_cents"], p["id"]),
    )
    return (same_category + others)[:limit]
