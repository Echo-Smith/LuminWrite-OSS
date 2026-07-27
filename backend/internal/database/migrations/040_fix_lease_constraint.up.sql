-- Fix: Remove the overly-broad unique constraint uk_active_lease that prevents
-- re-acquiring a lease after it has been released. The partial unique index
-- idx_active_lease_unique (WHERE status = 'active') is the correct constraint.

ALTER TABLE editorial_agent_leases
    DROP CONSTRAINT IF EXISTS uk_active_lease;
