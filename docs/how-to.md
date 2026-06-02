# Resolvarr: How to use it

This guide grows with every release. Each section answers three questions:

- **What is it?** In one sentence.
- **Why would I use it?** Who benefits and what problem it solves.
- **How do I set it up?** Step-by-step.

---

## Settings → Instances

**What is it?**
The list of Radarr and Sonarr servers that resolvarr acts on.

**Why would I use it?**
Every other feature in resolvarr needs to know where your Radarr and Sonarr live. You'll add each one here once, then use them everywhere.

**How do I set it up?**

1. Open resolvarr at `http://<your-server>:6075`
2. Go to **Settings → Instances**
3. Click **+ Add Instance**
4. Fill in the form:
   - **Name.** A label only you see (for example, "Radarr HD" or "Sonarr 4K").
   - **Type.** Radarr or Sonarr.
   - **Icon.** Standard or 4K, to match your setup visually.
   - **URL.** Where your Radarr/Sonarr runs. Examples:
     - `http://radarr:7878` if resolvarr and Radarr are on the same Docker network
     - `http://192.168.1.50:7878` for a direct IP on your LAN
   - **API Key.** Find it in Radarr/Sonarr under **Settings → General → Security → API Key**.
5. Click **Test**. The result shows "OK" with the Radarr/Sonarr version.
6. Click **Save**.

After saving, the instance row shows **Connected** (green) if resolvarr can reach it. Resolvarr rechecks every minute.

If it says **Failed**, hover the message or click **Edit** → **Test** to see the exact error.

---

## Settings → Notifications

**What is it?**
Notification agents that get a message when resolvarr does something (tags added, recovery runs, errors). Currently supported: Discord, Gotify, NTFY, Pushover, and Apprise.

**Why would I use it?**
So you know when something happens without having to watch logs. Optional. Skip the section if you don't want notifications.

**How do I set it up?**

The steps below show Discord, which is the most common case. The other agents have their own setup flow on the same **Settings → Notifications** page (each agent type asks for the URL or token it needs).

1. In Discord, open the channel you want messages in
2. Channel settings → **Integrations → Webhooks → New Webhook**
3. Copy the webhook URL
4. In resolvarr, go to **Settings → Notifications** and add a new agent of type **Discord**
5. Paste the URL into the Webhook URL field
6. Click **Test**. A blue embed should appear in your channel.
7. Enable the agent and pick which events it should fire on (downloads, imports, errors, scheduled scans, and so on)

---

## Settings → Display

**What is it?**
Lets you pick how big the UI text is.

**Why would I use it?**
On a big monitor or from across the room, **Large** is easier to read. On a laptop, **Compact** fits more on screen.

**How do I set it up?**

1. Go to **Settings → Display**
2. Pick **Compact**, **Default**, or **Large**
3. The UI resizes immediately. No save button, no reload.

Your choice is saved server-side, so every browser that connects to this resolvarr sees the same scale.

---

## Tags

**What is it?**
A list of every tag on a chosen Radarr or Sonarr, with how many items use each one. You can rename a tag (and all items using it get the new name) or delete a tag (and it's removed from every item).

**Why would I use it?**
Tags pile up over time. Old release groups you don't care about anymore, typos, naming changes. This section is the fastest way to tidy up without clicking through every movie.

**How do I set it up?**

1. Go to the **Tags** tab (top of the page)
2. Pick the instance you want to clean up (for example, "Radarr HD")
3. The list shows every tag, sorted by how many items use it (most-used first). Click a column header to sort by name or by count instead.

**Rename a tag:**

1. Click **Rename** on a row
2. Type the new label. Fix a typo, change casing, or merge two tags by renaming to an existing name.
3. The preview table shows every movie or series affected, with the old and new tag side by side.
4. Optional: check **Keep the old tag after renaming** if you want to keep the old name as an empty tag.
5. Click **Rename**, or **Merge** if the new name is already a tag.

**Merging:** if the new name matches a tag that already exists, resolvarr won't create a duplicate. The items get the existing tag instead, and the old one is removed. The button says "Merge" so you know this is what's happening.

Every movie or series that had the old tag keeps the tag, just with the new name. You don't have to retag anything manually.

(Behind the scenes, Radarr and Sonarr can't actually edit a tag's label, so resolvarr makes a new tag, moves items over, and deletes the old one. That's what you'd see if you were watching the API calls, but from the UI it's just a rename.)

**Delete a tag:**

1. Click **Delete** on a row
2. The preview table shows every movie or series that has the tag
3. Optional: check **Keep the tag definition** if you want to clear the tag off every item but keep the tag in Radarr/Sonarr for later use.
4. Click **Delete**. The tag is removed from every item and (unless you kept it) from Radarr/Sonarr entirely.

**Bulk delete:**

1. Check the boxes for the tags you want gone
2. Click **Delete selected**
3. Confirm. Resolvarr goes through them one by one.

**Coexistence:** This replaces the bash scripts `tagarr_remove.sh` and `tagarr_rename.sh`. You can stop using those if all you needed was cleanup.

---

## More sections coming

This guide currently covers the basics: instance setup, notifications, display, and tag cleanup. The Features section in the README lists everything resolvarr can do today. Dedicated How-to sections for Library scan (Tag library, Discover, Recover, Audio, Video, DV detail, Missing episodes, TBA refresh, Plex label sync), Schedules, Quick fix-all, and Webhooks land here as the guide grows.
