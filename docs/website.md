# The website

`site/` is the whole website: plain HTML, one CSS file, one tiny script (the Copy button), no build step
and nothing loaded from another host. It is a retro Mac desktop on purpose. Its voice is the one in
[AGENTS.md](../AGENTS.md#voice), and it says the same thing as the README.

| File | What it is |
|---|---|
| `site/index.html` | the page. Title, description, Open Graph and JSON-LD (the app and the FAQ) are in its head |
| `site/style.css` | all of the styling |
| `site/site.js` | the Copy button. Without JavaScript the button simply is not shown |
| `site/404.html` | "Sorry, a system error occurred." |
| `site/og.png` | the 1200x630 card social networks show. Re-take it when the headline changes |
| `site/llms.txt` | the same story for coding agents (`llms.txt` convention), with links to the docs |
| `site/robots.txt`, `site/sitemap.xml` | for crawlers |

## Preview

Open the file; there is nothing to start. In Veld: `veld ide open site/index.html --notify` (it reloads
when you save). Any browser works too: `open site/index.html`.

## Checks

`python3 tests/validate-site.py` (and CI, in the Lint job) keeps what makes the page findable and honest:
one h1, a title of at most 60 characters, a description of at most 160, an absolute canonical that is the
one in the sitemap and robots.txt, JSON-LD that parses and whose FAQ is exactly the questions on the page,
every local link existing, nothing inline or remote (the page's content-security policy forbids it), a
1200x630 `og.png`, and the install line identical in the README, `docs/install.md`, the page and `llms.txt`.

## Going live

`.github/workflows/pages.yml` publishes `site/` to GitHub Pages on every push to `main` that touches it
(or by hand: Actions > Website > Run workflow). It uploads the folder as it is. It needs the repository's
Pages source set to **GitHub Actions** (Settings > Pages), which only an admin can switch on:

```sh
gh api -X POST repos/prosperity-solutions/outfit/pages -f build_type=workflow
```

### The domain

The site is `https://outfit.oss.life.li/`. At the DNS host of `life.li`, add:

```
outfit.oss  CNAME  prosperity-solutions.github.io.
```

then tell GitHub (once the record resolves; it issues the certificate by itself):

```sh
gh api -X PUT repos/prosperity-solutions/outfit/pages -f cname=outfit.oss.life.li -F https_enforced=true
```

The canonical URL, `og:url`, `og:image`, the sitemap and `llms.txt` all name that host. To move the site
to another domain, change them together (the site check fails when they disagree) and re-point the DNS.
After it is live, submit `https://outfit.oss.life.li/sitemap.xml` in Google Search Console and Bing
Webmaster Tools, and look at the page in Google's Rich Results test.
