# PredictMarket Hosted UI Prototype

This directory is a zero-dependency, mobile-first prototype for the hosted UI
described in `V3_PLAN.md`. Serve it from the same origin as the API, or
through a reverse proxy that exposes `/api/user/*`. The shell now exchanges a
one-time launch token and renders live user, event, market, and order data.

Routes use URL hashes, so the prototype works inside an iframe without server
side rewrites:

- `#/home` — discovery page
- `#/category/:category` — category and market list
- `#/event/:event` — event detail and related markets
- `#/market/:market` — market detail and outcome selection
- `#/orders` — authenticated order history

For iframe integration pass a trusted parent origin as `parent_origin`, for
example `index.html?parent_origin=https%3A%2F%2Fmerchant.example`. Events are
posted only to that HTTPS origin. See `docs/HOSTED_FRONTEND.md` for the V3 API
and Launch integration contract.
