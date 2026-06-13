export const DEFAULT_MANUAL_LEASE_TTL_SECONDS = 4 * 60 * 60;
export const MANUAL_LEASE_HOLDER_PREFIX = "manual:";

interface ManualLeaseLike {
  leaseActive?: boolean;
  leaseKind?: string;
  leaseHolder?: string;
}

export function manualLeaseHolder(username?: string) {
  const trimmed = username?.trim();
  return `${MANUAL_LEASE_HOLDER_PREFIX}${trimmed || "dashboard"}`;
}

export function isManualLease(lease: ManualLeaseLike) {
  if (!lease.leaseActive) return false;
  if (lease.leaseKind === "manual") return true;
  return !!lease.leaseHolder?.startsWith(MANUAL_LEASE_HOLDER_PREFIX);
}
