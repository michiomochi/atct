# Goal 268: Review Receive Transition Guard

## Problem

A review rejection clears `review_received_by` and `review_received_at` so the next
review cycle can start. The three review-receive SQL mutations (goal, plan, task)
currently test only that the receipt is empty. Before the submitter receives the
rejection and explicitly reissues review, a delayed or duplicate review-receive
therefore writes a new receipt into the rejected cycle.

## Scope

Guard the store's three review-receive mutations. A review receive is legal only
while a review request is open: it has been requested, has no receipt, has not
been rejected, and has not completed or recovered where that lifecycle supports
recovery. The failing mutation returns its existing required-state error and
publishes no event.

`Receive*HandoffReviewRejection` remains the only legal receipt after rejection.
After it succeeds, `Request*HandoffReview` clears rejection state for the next
cycle; the normal review receive remains legal in that reissued cycle.

## Design

Add `review_rejected_at IS NULL` to the WHERE clause of these generated-query
sources:

- `ReceiveTaskHandoffReview`
- `ReceiveGoalHandoffReview`
- `ReceivePlanHandoffReview`

The Store methods already translate zero affected rows into each lifecycle's
required-state error, so no new error type, RPC behavior, monitor reconciliation,
or delivery logic is needed. This deliberately excludes Goal 265's stale-monitor
delivery/reconciliation responsibility.

## Verification

Extend each existing lifecycle test to attempt a delayed review receive immediately
after rejection. Assert the required-state error and compare the persisted
rejection/receipt fields before and after the failed call. Keep the test's existing
rejection-receive, reissue, valid receive, and completion assertions, proving the
permitted path has not changed.
