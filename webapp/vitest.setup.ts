import '@testing-library/jest-dom/vitest'
import { cleanup } from '@solidjs/testing-library'
import { afterEach, beforeEach } from 'vitest'

// @solidjs/testing-library registers its own cleanup only when vitest runs
// with `globals: true`, which this project does not. Without it, a second
// render leaves the first one in the document and every query finds two of
// everything — a failure that reads as a duplicate-element bug in the
// component rather than as leftover state.
afterEach(cleanup)

// The router reads the real history, so a test that navigates leaves the
// next test starting on whatever route it ended on.
beforeEach(() => {
  window.history.pushState({}, '', '/')
})
