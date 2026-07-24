# Commerce

Commerce is the shared transaction context for orders, provider-confirmed money movement, points, entitlements, and
virtual-goods delivery. Product catalogs remain in their owning products; Commerce records immutable purchase snapshots.

## Language

**Buyer**:
The user or guest identity that places an Order.
_Avoid_: Customer account, subscriber

**Order**:
A buyer's priced purchase intent and the lifecycle of fulfilling that intent.
_Avoid_: Payment, transaction

**Order Item Snapshot**:
The immutable commercial and delivery description captured from a product catalog when an Order is created.
_Avoid_: Product, SKU

**Payment Attempt**:
One provider-specific attempt to collect the amount of an Order.
_Avoid_: Order, payment session

**Provider Event**:
An authenticated, immutable observation received from or fetched from a payment provider.
_Avoid_: Callback payload, webhook row

**Settlement**:
Provider-confirmed capture or receipt of funds for a Payment Attempt.
_Avoid_: Callback success, fulfilled order

**Reconciliation**:
The process of comparing provider-authoritative facts with Commerce facts and applying the one safe convergent outcome.
_Avoid_: Retry, sync

**Refund**:
A merchant-initiated request and provider-confirmed return of settled funds.
_Avoid_: Cancellation, reversal

**Dispute**:
A provider case that can reverse or hold settled funds independently of a merchant Refund.
_Avoid_: Refund, chargeback callback

**Entitlement**:
A durable, revocable right for a Buyer to access a purchased product.
_Avoid_: Delivery link, download token

**Delivery Grant**:
A bounded, revocable credential that exercises an Entitlement for a particular delivery.
_Avoid_: Entitlement, purchase

**Recovery Action**:
An idempotent operator or scheduled instruction to query, reconcile, refund, or revoke without rewriting provider facts.
_Avoid_: Status edit, force success
