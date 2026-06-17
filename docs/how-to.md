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
3. **Step 2**: build one or more rules. Each rule is a set of tags plus the quality profile matching items should use. Combine tags with AND / OR (AND binds first, so "A AND B OR C" means "(A AND B) OR C"). Examples: "anime OR cartoon" goes to the Anime profile; "anime AND uhd" goes to the Anime UHD profile.
4. **Step 3**: review your rules, then run.
5. The result shows every switch (which profile to which, and which rule matched). On a Preview you can hit Apply to write them.

**Notes:**
- If an item's tags match two rules that point to different profiles, it is shown as a conflict and left unchanged, so nothing switches ambiguously.
- Switching the profile does not start a search. Radarr or Sonarr will pick up upgrades on their own next search if the new profile allows a better release.
- Running it on a schedule, and when items are added or imported, is on the way.

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
