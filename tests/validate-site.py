#!/usr/bin/env python3
"""Checks that the website (site/) keeps what makes it findable and honest. Standard library only.

Run from the repository root: python3 tests/validate-site.py

  - one h1; a title of at most 60 characters; a description of at most 160
  - the canonical URL is absolute and is the one in sitemap.xml and robots.txt
  - every JSON-LD block parses, and the FAQ data is exactly the questions on the page
  - every local href/src exists; nothing is loaded from another host (the page's CSP forbids it)
  - og.png is 1200x630
  - the install line is the same in README.md, docs/install.md, site/index.html and site/llms.txt
    (AGENTS.md, Voice: one message in every place)
"""
import html
import json
import os
import re
import struct
import sys

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
SITE = os.path.join(ROOT, "site")
bad = []


def fail(msg):
    bad.append(msg)


def read(*p):
    with open(os.path.join(ROOT, *p), encoding="utf-8") as f:
        return f.read()


page = read("site", "index.html")

title = re.search(r"<title>(.*?)</title>", page, re.S).group(1).strip()
if len(title) > 60:
    fail(f"title is {len(title)} characters (60 at most, search results cut it): {title!r}")
m = re.search(r'<meta name="description" content="(.*?)"', page)
if not m or not 50 <= len(html.unescape(m.group(1))) <= 160:
    fail("meta description must be 50 to 160 characters")
if len(re.findall(r"<h1[ >]", page)) != 1:
    fail("the page needs exactly one h1")

canon = re.search(r'<link rel="canonical" href="(.*?)"', page)
if not canon or not canon.group(1).startswith("https://"):
    fail("canonical must be an absolute https URL")
else:
    sm = read("site", "sitemap.xml")
    if canon.group(1) not in sm:
        fail("canonical is not in sitemap.xml")
    if canon.group(1).rstrip("/") + "/sitemap.xml" not in read("site", "robots.txt"):
        fail("robots.txt does not point at the sitemap on the canonical host")
    if canon.group(1) not in re.findall(r'<meta property="og:url" content="(.*?)"', page):
        fail("og:url differs from canonical")

blocks = re.findall(r'<script type="application/ld\+json">(.*?)</script>', page, re.S)
if not blocks:
    fail("no JSON-LD")
faq = None
for b in blocks:
    try:
        data = json.loads(b)
    except ValueError as e:
        fail(f"JSON-LD does not parse: {e}")
        continue
    if data.get("@type") == "FAQPage":
        faq = data
if faq is None:
    fail("no FAQPage JSON-LD")
else:
    ld = [(q["name"], q["acceptedAnswer"]["text"]) for q in faq["mainEntity"]]
    vis = [(html.unescape(q), html.unescape(a)) for q, a in
           re.findall(r"<details[^>]*>\s*<summary>(.*?)</summary>\s*<p>(.*?)</p>\s*</details>", page, re.S)]
    if ld != vis:
        fail("the FAQ JSON-LD is not exactly the questions and answers on the page")

for attr, ref in re.findall(r'(href|src)="([^"]*)"', page):
    if ref.startswith(("#", "mailto:")):
        continue
    if ref.startswith("https://") and attr == "href" and not ref.endswith((".css", ".js")):
        continue  # a link to another site (docs, GitHub, ...) is fine
    if ref.startswith(("http://", "https://", "//")):
        fail(f"{attr}={ref!r} loads from another host, which the page's CSP forbids")
    elif not os.path.exists(os.path.join(SITE, ref.split("#")[0])):
        fail(f"{attr}={ref!r} does not exist in site/")
if "<style" in page or re.search(r"<script(?![^>]*(src=|ld\+json))", page) or " style=" in page:
    fail("inline CSS/JS found; the CSP only allows files from the site itself")

with open(os.path.join(SITE, "og.png"), "rb") as f:
    head = f.read(24)
if head[:8] != b"\x89PNG\r\n\x1a\n" or struct.unpack(">II", head[16:24]) != (1200, 630):
    fail("og.png must be a 1200x630 PNG")

cmd = "curl -fsSL https://raw.githubusercontent.com/prosperity-solutions/outfit/main/install.sh | sh"
for p in (("README.md",), ("docs", "install.md"), ("site", "index.html"), ("site", "llms.txt")):
    if cmd not in read(*p):
        fail(f"the install line is not in {'/'.join(p)} (one message, everywhere)")

if bad:
    print("site check FAILED:\n  - " + "\n  - ".join(bad))
    sys.exit(1)
print("site OK: title %d chars, %d FAQ entries, canonical %s" % (len(title), len(faq["mainEntity"]), canon.group(1)))
