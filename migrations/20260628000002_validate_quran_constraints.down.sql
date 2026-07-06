-- No-op: Postgres has no way to un-validate a CHECK constraint (VALIDATE only flips
-- convalidated=false -> true; there is no INVALIDATE). The constraints themselves are
-- dropped/recreated by the down migrations of 20260627000002 and 20260628000001, so
-- rolling those back already removes the validated state. Nothing to undo here.
SELECT 1;
