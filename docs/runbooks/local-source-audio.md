# Local source audio

Use this procedure to serve a source-catalog audio collection from verified
originals on the engine host instead of from the source. The source-streaming
collection stays as it is; the local copy is a second engine collection with
its own policy, byte budget, free-space reserve and independent recovery
evidence. Democracy Now! (`democracy_now_vid`) stays source-streaming only: the
engine refuses a local collection for it and refuses its sources in any local
review.

The examples use Woodroom's collection: Internet Archive collection
`dj-screw-discography`, engine collection `dj-screw-discography-local`. Run the
`vhness` commands where the engine's CLI runs, with `DATABASE_URL` and an
absolute `VHNESS_MEDIA`. The collection must be in the instance's
`VHNESS_COLLECTIONS` scope.

## 1. Copy and check the originals

Place each original at
`$VHNESS_MEDIA/dj-screw-discography-local/<identifier>/<file name>`, using the
exact Internet Archive identifier and file name. The directory name is the
engine collection, not the source collection. Files must be regular files, not
links. The copy must match the source's size and MD5; review checks both again.

Keep an independent copy on another host with the same layout below its own
collection directory.

## 2. Write the recovery receipt

On the independent host, over its collection directory:

```sh
python3 scripts/source_audio_receipt.py --collection dj-screw-discography-local \
  --root /path/to/independent/dj-screw-discography-local \
  --output /private/operator/dj-screw-local-receipt-2026-11-21.json --host nuc
```

The script needs only Python 3, reads the copy without changing it, and prints
the receipt's SHA-256. Copy the receipt to the engine host and keep it with the
operator's private records, outside Git. The receipt is JSON:

```json
{"collection": "dj-screw-discography-local", "host": "nuc",
 "location": "/path/to/independent/dj-screw-discography-local",
 "verified_at": "2026-11-21T12:00:00Z",
 "files": [{"path": "<identifier>/<file name>", "bytes": 123, "sha256": "<64 hex>"}]}
```

Unknown fields, duplicate paths, absolute or `..` paths and other collections
are refused. Each approved review records the SHA-256 of the receipt's exact
bytes as `independent_recovery_sha256`. That proves the receipt listed matching
checksums when the review ran; it is not a restore drill.

## 3. Create the collection

Choose a budget that covers everything under the collection directory, which
review compares with the budget as acquisition does, and a reserve to keep free
on the volume:

```sh
du -sb "$VHNESS_MEDIA/dj-screw-discography-local"
vhness collection --collection dj-screw-discography-local --policy local_preserve \
  --budget BYTES --reserve BYTES --source-collection dj-screw-discography
```

`--source-collection` sets which Internet Archive collection discovery queries
and which membership imports require. It cannot be changed later; rerunning
the command with the same values does nothing. Raise limits only through
`review-budget`.

## 4. Import the source catalog

Discovery is metadata only and resumes across runs. Repeat until the status
reports `discovery_complete` with nothing pending:

```sh
vhness discover-catalog --collection dj-screw-discography-local --pages 10 --items 400 --timeout 20m
```

Listing exclusions, corrections and artist profiles belong to each collection.
Apply the ones the streaming collection has with `set-source-catalog-listing`,
`edit-source-catalog` and `import-source-artist` for the new collection.

## 5. Review the originals

```sh
vhness review-source-audio-batch --collection dj-screw-discography-local \
  --items 100 --timeout 30m \
  --recovery-receipt /private/operator/dj-screw-local-receipt-2026-11-21.json \
  --notes 'Owner approved self-hosted playback of verified originals'
```

Each run hashes the selected originals of up to `--items` sources and prints the
collection's coverage plus `skipped`: each source it did not approve, with its
state and reason. A source with any missing, changed or unrecorded file gets no
approval. Fix the copy (and write a new receipt when the independent copy
changed), then wait an hour for the batch to retry it, or review it at once:

```sh
vhness review-source-audio --collection dj-screw-discography-local --id IDENTIFIER \
  --recovery-receipt /private/operator/dj-screw-local-receipt-2026-11-21.json \
  --notes 'Owner approved self-hosted playback of verified originals'
```

Repeat the batch until `pending` and `retry` are zero.

## 6. Serve

The engine serves reviewed originals from
`/api/catalog/v1/items/{item}/representations/audio/media`, the URL the resolver
returns for the local collection. Each request checks the current review. The
server hashes an original on first use and again when its file changes or after
24 hours. A changed original returns 503 until it is restored. Browser
playback is a separate check.

A consumer switches by pointing its collection at the local collection and
adopting a core with this support. Viewers older than this change reject local
file names that contain `..`, such as `Track.. name.mp3`.
