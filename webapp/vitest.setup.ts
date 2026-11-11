import '@testing-library/jest-dom/vitest'
import { afterEach, beforeEach } from 'vitest'
import { defineZest } from '~/ui/zest'
import { stopRouter } from '~/app/router'

// Importing a Zest package defines nothing, so the tags are defined here,
// as index.ts does in the browser.
defineZest()

// The router reads the real history, so a test that navigates leaves the
// next test starting on whatever route it ended on.
beforeEach(() => {
  window.history.pushState({}, '', '/')
})

// A test leaves its elements in the document. Without this, a second test
// finds two of everything.
//
// Do not import a module here that imports ~/lib/api (lib/session does):
// a setup import loads the real client before a test's vi.mock applies,
// and the test then talks to a real server.
afterEach(() => {
  document.body.replaceChildren()
  stopRouter()
})
