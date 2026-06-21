# Tagarr — How to use it

This guide grows with every release. Each section answers three questions:

- **What is it?** — in one sentence
- **Why would I use it?** — who benefits and what problem it solves
- **How do I set it up?** — step-by-step

---

## Settings → Instances

**What is it?**
The list of Radarr and Sonarr servers that Tagarr will eventually act on.

**Why would I use it?**
Every other feature in Tagarr needs to know where your Radarr and Sonarr live. You'll add each one here once, then use them everywhere.

**How do I set it up?**

1. Open Tagarr at `http://<your-server>:6075`
2. Go to **Settings → Instances**
3. Click **+ Add Instance**
4. Fill in the form:
   - **Name** — a label only you see (e.g. "Radarr HD", "Sonarr 4K")
   - **Type** — Radarr or Sonarr
   - **Icon** — Standard or 4K (matches your setup visually)
   - **URL** — where your Radarr/Sonarr runs. Examples:
     - `http://radarr:7878` — if Tagarr and Radarr are on the same Docker network
     - `http://192.168.1.50:7878` — direct IP on your LAN
   - **API Key** — find it in Radarr/Sonarr under **Settings → General → Security → API Key**
5. Click **Test** — you should see "OK — v<version>"
6. Click **Save**

After saving, the instance row shows **Connected** (green) if Tagarr can reach it. Tagarr rechecks every minute.

If it says **Failed**, hover the message or click **Edit** → **Test** to see the exact error.

---

## Settings → Notifications

**What is it?**
A Discord webhook that will get a message when Tagarr does something (tags added, recovery runs, errors).

**Why would I use it?**
So you know when something happens without having to watch logs. Optional — leave it empty if you don't use Discord.

**How do I set it up?**

1. In Discord, open the channel you want messages in
2. Channel settings → **Integrations → Webhooks → New Webhook**
3. Copy the webhook URL
4. In Tagarr, go to **Settings → Notifications**
5. Paste the URL into **Webhook URL**
6. Click **Test** — a blue embed should appear in your channel
7. Check **Enable** to turn notifications on

**Note:** v0.1 doesn't send any automated notifications yet — this is just the connection. Automation arrives as tagging features are added.

---

## Settings → Display

**What is it?**
Lets you pick how big the UI text is.

**Why would I use it?**
On a big monitor or from across the room, **Large** is easier to read. On a laptop, **Compact** fits more on screen.

**How do I set it up?**

1. Go to **Settings → Display**
2. Pick **Compact**, **Default**, or **Large**
3. The UI resizes immediately — no save button, no reload

Your choice is saved server-side, so every browser that connects to this Tagarr sees the same scale.

---

## Tags

**What is it?**
A list of every tag on a chosen Radarr or Sonarr, with how many items use each one. You can rename a tag (and all items using it get the new name) or delete a tag (and it's removed from every item).

**Why would I use it?**
Tags pile up over time — old release groups you don't care about anymore, typos, naming changes. This section is the fastest way to tidy up without clicking through every movie.

**How do I set it up?**

1. Go to the **Tags** tab (top of the page)
2. Pick the instance you want to clean up (e.g. "Radarr HD")
3. The list shows every tag, sorted by how many items use it (most-used first). Click a column header to sort by name or by count instead.

**Rename a tag:**

1. Click **Rename** on a row
2. Type the new label — fix a typo, change casing, or merge two tags by renaming to an existing name
3. The preview table shows every movie or series affected, with the old and new tag side by side
4. Optional: check **Keep the old tag after renaming** if you want to keep the old name as an empty tag
5. Click **Rename** — or **Merge** if the new name is already a tag

**Merging:** if the new name matches a tag that already exists, Tagarr won't create a duplicate. The items get the existing tag instead, and the old one is removed. The button says "Merge" so you know this is what's happening.

Every movie or series that had the old tag keeps the tag — just with the new name. You don't have to retag anything manually.

(Behind the scenes Radarr and Sonarr can't actually edit a tag's label, so Tagarr makes a new tag, moves items over, and deletes the old one. That's what you'd see if you were watching the API calls, but from the UI it's just a rename.)

**Delete a tag:**

1. Click **Delete** on a row
2. The preview table shows every movie or series that has the tag
3. Optional: check **Keep the tag definition** if you want to clear the tag off every item but keep the tag in Radarr/Sonarr for later use
4. Click **Delete** — the tag is removed from every item and (unless you kept it) from Radarr/Sonarr entirely

**Bulk delete:**

1. Check the boxes for the tags you want gone
2. Click **Delete selected**
3. Confirm — Tagarr goes through them one by one

**Coexistence:** This replaces the bash scripts `tagarr_remove.sh` and `tagarr_rename.sh`. You can stop using those if all you needed was cleanup.

---

## Library scan → Profile switcher

**What it does:** automatically switches a movie or series to a different quality profile based on the tags it already carries in Radarr or Sonarr.

**Why:** if you tag items (by hand, by an import list, by Radarr/Sonarr auto-tagging, or by resolvarr's own tagging), you can let those tags decide the quality profile, instead of changing each item by hand.

**How:**

1. Open Library scan and pick the **Profile switcher** tab, then "Configure and run".
2. **Step 1**: choose the instance and a run mode: Preview (show what would change) or Apply (write the changes).
3. **Step 2**: build one or more rules. Each rule is a set of tags plus the quality profile matching items should use. Combine tags with AND / OR (AND binds first, so "A AND B OR C" means "(A AND B) OR C"), and flip a tag to NOT to require that the item does not have it. Examples: "anime OR cartoon" goes to the Anime profile; "anime AND uhd" goes to the Anime UHD profile; "anime AND NOT remux" goes to the Anime profile only when the item is not a remux.
4. **Step 3**: review your rules, then run.
5. The result shows every switch (which profile to which, and which rule matched). On a Preview you can hit Apply to write them.

**Notes:**
- If an item's tags match two rules that point to different profiles, it is shown as a conflict and left unchanged, so nothing switches ambiguously.
- Switching the profile does not start a search. Radarr or Sonarr will pick up upgrades on their own next search if the new profile allows a better release.
- Running it on a schedule, and when items are added or imported, is on the way.

---

## qBittorrent Season/Episode tagging

**What it does:** tags each qBittorrent download as Season, Episode, or leaves it untagged, so you can sort or filter your TV downloads in qBittorrent.

**Why:** the tag tells you at a glance whether a download is a single episode or a full season pack, without opening it.

**How it decides:** instead of guessing only from the release name, resolvarr looks at the download's actual contents:

- A download with several episode files is a **Season** pack.
- A download with one video file is an **Episode** (this also catches single episodes whose name only has an absolute number, like `Show - 10`, which a name-only check would miss).
- Subtitles, `.nfo` files, and sample clips are not counted, so a 24-episode pack reads as 24 video files, not 48.
- If your Radarr/Sonarr instances are set up, resolvarr also reads the categories they assign in qBittorrent to tell a movie from a series, so a movie is never tagged as a season.

**Where it runs:** a one-off or scheduled Library scan, the Sonarr grab, and qBittorrent's own "run on add" hook (which also covers cross-seed and manual downloads). The backlog scan's preview shows the video-file count, total size, and a short reason for each decision.

---

## Webhooks (Sonarr/Radarr Connect)

**What it does:** lets Sonarr and Radarr tell resolvarr the moment something happens (a grab, an import, a file delete), so your rules run automatically on that event instead of only on a manual or scheduled scan.

**Why:** the action happens right when the release arrives, while the data you want to fix (release group, language tag, season/episode) is still known.

**How to set it up:**

1. On the Webhooks page, open an instance and click **Set up webhook** to get its URL.
2. In Sonarr/Radarr → Settings → Connect, add a Webhook connection and paste that URL.
3. Add one or more **rules** on the instance (the + Add rule button) to pick which functions fire (tag, grab rename, recover, season/episode tag, and so on).

The **Recent activity** tab shows the Connect events as they arrive, with what each rule did. The separate **qBit webhook** tab shows torrents qBittorrent reported directly (cross-seed and manual adds).

### One Sonarr/Radarr feeding several qBittorrent clients

If a single Sonarr or Radarr sends downloads to more than one qBittorrent instance (for example you route items to different clients by tag), give each rule its own webhook URL so each one only ever sees its own grabs. That avoids every rule firing on every grab and reporting "not found" for the torrents that live in a different client.

1. In resolvarr, create one rule per qBittorrent instance. On each rule, use the button that gives it its own dedicated webhook URL.
2. In Sonarr/Radarr → Settings → Connect, add one Webhook connection per rule and paste that rule's URL.
3. On each connection, set the **Tags** field to the same tag you use to route items to that download client.

Sonarr/Radarr then sends each grab only to the connection whose tag matches, so each resolvarr rule fires only for the items meant for its qBittorrent instance. A rule that uses its own URL never fires from the shared instance URL, so it never trips over a torrent that lives in a different client.

---

## Coming later

These sections will be added as the feature ships:

- **Release Groups** — add/edit/enable which release groups get tagged
- **Filters** — pick which quality sources and audio codecs count as "good"
- **Discovery** — see new release groups Tagarr finds and approve them
- **Recovery** — batch-fix missing release groups from grab history
- **Tags** — overview of all tags on each instance, remove/rename in bulk
- **History** — searchable log of everything Tagarr did
- **Scheduling** — run scans/recovery on an interval

Each new feature will also arrive with a coexistence note: does it replace one of the bash scripts, or does it run alongside them?
