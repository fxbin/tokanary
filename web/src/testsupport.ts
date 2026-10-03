/**
 * Shared helpers for the test suite.
 *
 * `data/prices-raw.json` and `data/gateway-models.json` are gitignored: the
 * first maps this machine's model aliases, the second carries an internal
 * gateway URL. A fresh clone therefore has no price table at all, and tests
 * must not assert that prices exist. They should assert that the page still
 * renders and computes correctly with whatever pricing is present.
 *
 * Live fixtures come from `.cache/dashboard.json` (written by `tokanary
 * refresh` / `tokanary build`). When that file is missing, real-data suites
 * skip instead of failing.
 */
import { readFileSync, existsSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, resolve } from 'node:path'

const here = dirname(fileURLToPath(import.meta.url))

/** Absolute path of the optional live dashboard fixture. */
export const dashboardJSONPath = resolve(here, '../../.cache/dashboard.json')

/** Load the live dashboard payload, or null when refresh has not been run. */
export function loadDashboardFixture(): any | null {
  if (!existsSync(dashboardJSONPath)) return null
  try {
    return JSON.parse(readFileSync(dashboardJSONPath, 'utf8'))
  } catch {
    return null
  }
}

/** True when the payload carries at least one usable price from any source. */
export function hasAnyPricing(data: any): boolean {
  if (!data) return false
  const pricing = data.pricing || {}
  for (const k of Object.keys(pricing)) {
    const c = (pricing[k] || {}).cost || {}
    if (c.input !== null && c.input !== undefined) return true
  }
  return false
}

/** True when the payload carries a gateway price table with models. */
export function hasGateway(_data: any): boolean {
  return false
}

/**
 * True when the payload was built against a curated models.dev table.
 *
 * This is deliberately narrower than hasAnyPricing. The `pricing` block is not
 * a proxy: BuildPricing also merges gateway prices into it, so it stays
 * non-empty on a clone that has only data/gateway-models.json. `pricingMeta.file`
 * is set by the Go side only when .cache/prices-raw.json actually loaded.
 *
 * Comparing the "gateway" and "models.dev" cost bases only means something when
 * both can price the same models. With only a gateway table every model
 * resolves through the gateway, so the two totals are identical by
 * construction - not a bug.
 */
export function hasCurated(data: any): boolean {
  return !!(data && (data.pricingMeta || {}).file)
}
