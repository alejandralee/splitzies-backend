# Backend work for the splitzies-app review/tax-tip/summary rework

Three features in the frontend (`~/splitzies-app`) are client-only because the
API doesn't support them. Add the backend for them.

Everything below marked **verified** was checked against this repo on
2026-09-01. Everything else is a claim from the frontend side — confirm before
acting on it.

---

## 1. Item CRUD (the blocking one)

The review screen lets people rename an item, change its price, add units, and
remove units. None of it persists: `lib/api.ts` has no item write endpoints, so
the frontend mutates local state and mints client-side ids of the form
`local-<uuid>`. If that bill is later shared, claiming a locally-added item
POSTs an assignment for an item id the server has never seen.

**Verified:** items are one row per physical unit — `persistence.ReceiptItem`
has `GroupID`, `GroupName`, `Name`, `Amount`, `DisplayOrder`, and `SaveReceipt`
already explodes a parsed `ReceiptItemDB{Quantity: 3}` into three rows sharing a
group. Grouping is display-only; assignment and splitting are always per unit.
Preserve that — the frontend's quantity stepper is just "how many rows are in
this group".

**Verified:** `GET /receipts/{receipt_id}/items` exists
(`GetReceiptItemsHandler`). There are no POST/PATCH/DELETE counterparts.

Needed:

- `POST /receipts/{receipt_id}/items` — create one unit; return the full item
  with its server id and `display_order`. Accepts an optional `group_id` so the
  stepper's `+` adds a unit to an existing group rather than starting a new one.
- `PATCH /receipts/{receipt_id}/items/{item_id}` — name and/or amount.
- `DELETE /receipts/{receipt_id}/items/{item_id}` — must cascade its
  assignments.

The UI edits name and price per *group*, applying to every unit in it, so a bulk
form may be better than making the client fan out N calls:
`PATCH /receipts/{receipt_id}/item-groups/{group_id}` taking name and/or unit
amount. Your call — say which way you went and why.

Watch for:
- `display_order` on insert.
- Currency consistency with the receipt's other amounts. Note
  `persistence.ReceiptItem.Amount` is a bare `float64` while the transport layer
  uses `money.Amount` — follow whatever the existing conversion does rather than
  inventing a second path.
- Whether removing the last unit of a group needs any further cleanup.

---

## 2. tax and tip on GetReceiptResponse

**Verified:** `PatchReceiptRequest` accepts `tax` and `tip`, but
`GetReceiptResponse` (transport/receipt_types.go:90) carries only `receipt_id`,
`users`, `items`, `assignments`. Nothing reads them back.

Consequence: `app/bill/[receiptId]/page.tsx` hardcodes `tax = 0; tip = 0` with a
comment saying they're "carried over from review step only", so a shared bill's
summary shows no tax or tip at all.

Add both to `GetReceiptResponse`.

---

## 3. extras_mode

New receipt-level setting, `"proportional" | "even"`, for how tax and tip get
divided. Default `"proportional"`. Needs a goose migration, a column on the
receipt, inclusion in `GetReceiptResponse`, and writability via the existing
`PATCH /receipts/{receipt_id}`.

Semantics the frontend implements today:

- **proportional** — each person's share of tax and tip is their claimed
  subtotal over the **full** item subtotal, *not* over the claimed portion. An
  unclaimed plate therefore keeps its tax and tip unassigned alongside it,
  instead of that money silently landing on whoever did claim something. This is
  deliberate: the summary screen reports the resulting gap as "Unassigned".
- **even** — tax and tip split equally across all people regardless of what they
  claimed.

**This is a decision to settle, not a spec to implement blindly.** The Claude
Design mockup this rework came from divides extras among claimers only, which
would make the gap disappear. The frontend currently does it the way described
above. Pick one and make both sides match.

**Verified:** `ComputeBillSplit(items, assignments)`
(transport/receipt_split.go:18) takes no tax or tip — it only splits item
amounts among assignees, and `TestComputeBillSplit_UnassignedItemExcluded`
covers the unassigned case. So tax/tip division is *entirely* a frontend concern
right now. Decide explicitly whether it should stay that way (server just stores
`extras_mode` for other clients to honour) or move server-side. If it moves,
`ComputeBillSplit` and its two callers — transport/receipt.go:254 and
transport/device.go:200, the latter computing a device's own total — both need
to change together.

---

## Conventions

**Verified:** routes are Go 1.22 method+path patterns registered on the mux in
`main.go` with `{receipt_id}` / `{user_id}` / `{item_id}` style params (note:
`receipt_id`, not `id`). Handlers live in `transport/`, hang off the transport
struct, and return JSON errors shaped
`{"error","code","request_id"}` (transport/errors.go). Queries are methods on
`persistence.Client` — no global DB state. Migrations are goose SQL in
`migrations/`.

Tests: match the existing handler-test style (`transport/*_test.go`).

---

## Before you write much

Show me the migration and the handler signatures first, plus your answer on the
per-item vs. per-group PATCH question and on where extras_mode division should
live.
