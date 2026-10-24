#!/usr/bin/env python3
"""
Lifel — Tavily AI Web Intelligence Utility
Supports Search, Extract, and Crawl operations via app.tavily.com.
Auto-loads TAVILY_API_KEY from environment or .env file.
"""

import os
import sys
import json
import urllib.request
import urllib.error
from pathlib import Path

BASE_DIR = Path(__file__).resolve().parent

def get_tavily_api_key() -> str:
    """Retrieve Tavily API Key from environment or .env file."""
    if os.getenv("TAVILY_API_KEY"):
        return os.getenv("TAVILY_API_KEY").strip()

    env_path = BASE_DIR / ".env"
    if env_path.exists():
        with open(env_path, "r", encoding="utf-8") as f:
            for line in f:
                line = line.strip()
                if line.startswith("TAVILY_API_KEY="):
                    return line.split("=", 1)[1].strip().strip('"').strip("'")

    config_path = BASE_DIR / "config.json"
    if config_path.exists():
        try:
            with open(config_path, "r", encoding="utf-8") as f:
                cfg = json.load(f)
                if cfg.get("tavily_api_key"):
                    return cfg["tavily_api_key"].strip()
        except Exception:
            pass

    return "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"

API_KEY = get_tavily_api_key()

def search(query: str, search_depth: str = "advanced", max_results: int = 5) -> dict:
    """Perform advanced web search using Tavily API."""
    try:
        from tavily import TavilyClient
        client = TavilyClient(api_key=API_KEY)
        return client.search(query=query, search_depth=search_depth, max_results=max_results)
    except ImportError:
        # Fallback to direct HTTP REST API (zero dependencies)
        req = urllib.request.Request(
            "https://api.tavily.com/search",
            headers={
                "Authorization": f"Bearer {API_KEY}",
                "Content-Type": "application/json",
            },
            data=json.dumps({
                "query": query,
                "search_depth": search_depth,
                "max_results": max_results,
                "include_answer": True,
            }).encode("utf-8"),
        )
        with urllib.request.urlopen(req, timeout=10) as resp:
            return json.loads(resp.read().decode("utf-8"))

def extract(urls: list[str]) -> dict:
    """Extract clean readable content from a list of URLs using Tavily Extract."""
    try:
        from tavily import TavilyClient
        client = TavilyClient(api_key=API_KEY)
        return client.extract(urls=urls)
    except ImportError:
        req = urllib.request.Request(
            "https://api.tavily.com/extract",
            headers={
                "Authorization": f"Bearer {API_KEY}",
                "Content-Type": "application/json",
            },
            data=json.dumps({
                "urls": urls,
                "extract_depth": "advanced",
                "format": "markdown",
            }).encode("utf-8"),
        )
        with urllib.request.urlopen(req, timeout=12) as resp:
            return json.loads(resp.read().decode("utf-8"))

def crawl(url: str, extract_depth: str = "advanced", limit: int = 5) -> dict:
    """Crawl a website domain to explore subpages using Tavily Crawl."""
    try:
        from tavily import TavilyClient
        client = TavilyClient(api_key=API_KEY)
        return client.crawl(url=url, extract_depth=extract_depth)
    except (ImportError, AttributeError):
        req = urllib.request.Request(
            "https://api.tavily.com/crawl",
            headers={
                "Authorization": f"Bearer {API_KEY}",
                "Content-Type": "application/json",
            },
            data=json.dumps({
                "url": url,
                "max_depth": 1,
                "limit": limit,
                "extract_depth": extract_depth,
                "format": "markdown",
            }).encode("utf-8"),
        )
        with urllib.request.urlopen(req, timeout=15) as resp:
            return json.loads(resp.read().decode("utf-8"))

if __name__ == "__main__":
    if len(sys.argv) < 2:
        print("Usage:")
        print("  python3 tavily_helper.py search <query>")
        print("  python3 tavily_helper.py extract <url1> [url2 ...]")
        print("  python3 tavily_helper.py crawl <url>")
        sys.exit(0)

    cmd = sys.argv[1].lower()
    if cmd == "search":
        q = " ".join(sys.argv[2:]) if len(sys.argv) > 2 else "Samsung Innovation Campus Indonesia"
        print(f"Searching: {q}")
        res = search(q)
        print(json.dumps(res, indent=2, ensure_ascii=False))
    elif cmd == "extract":
        targets = sys.argv[2:] if len(sys.argv) > 2 else ["https://en.wikipedia.org/wiki/Artificial_intelligence"]
        print(f"Extracting: {targets}")
        res = extract(targets)
        print(json.dumps(res, indent=2, ensure_ascii=False))
    elif cmd == "crawl":
        target = sys.argv[2] if len(sys.argv) > 2 else "https://docs.tavily.com"
        print(f"Crawling: {target}")
        res = crawl(target)
        print(json.dumps(res, indent=2, ensure_ascii=False))
    else:
        print(f"Unknown command: {cmd}")
