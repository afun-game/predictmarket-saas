# PredictMarket SaaS - MVP Implementation Plan

## Phase 1: Core Foundation (Week 1-2)

### Week 1: Project Setup & Database
- [x] Initialize Twill project structure
- [x] Create component interfaces
- [x] Design database schema
- [x] Implement database migrations
- [x] Setup local development environment
- [x] Create Docker Compose for dependencies

**Deliverables:**
- Working local environment
- Database schema deployed
- Basic project structure

### Week 2: Merchant & Auth
- [x] Implement Merchant service
  - [x] Registration logic
  - [x] API key generation (crypto/rand)
  - [x] API key validation
- [x] Create auth middleware
- [x] Add merchant CRUD endpoints
- [x] Write unit tests for merchant service

**Deliverables:**
- Merchant registration API
- API key authentication working
- Basic merchant management

## Phase 2: Event & Market (Week 3-4)

### Week 3: Event Service & Polymarket Integration
- [x] Implement Event service
- [x] Create Polymarket API client
- [x] Implement Polymarket sync job (Cron)
- [x] Add event CRUD endpoints
- [x] Setup Redis caching for events
- [x] Write integration tests

**Deliverables:**
- Events syncing from Polymarket
- Event listing and detail APIs
- Background sync job running

### Week 4: Market Service
- [x] Implement Market service
- [x] Add market creation logic (admin only)
- [x] Implement market listing with filters
- [x] Add liquidity pool management
- [x] Create market status transitions
- [x] Write unit tests

**Deliverables:**
- Market creation API
- Market listing with filters
- Basic liquidity management

## Phase 3: Trading Engine (Week 5-6)

### Week 5: Wallet Service
- [x] Implement Wallet service
- [x] Add balance tracking (available + locked)
- [x] Implement credit/debit operations
- [x] Add transaction recording
- [x] Implement balance locking for orders
- [x] Add transaction history API
- [x] Write unit tests

**Deliverables:**
- Virtual wallet system working
- Transaction history tracking
- Balance locking mechanism

### Week 6: Order Service & Matching
- [x] Implement Order service
- [x] Add order creation logic
- [x] Implement simple matching engine
  - [x] Price-time priority
  - [x] Immediate-or-cancel orders
- [x] Add order cancellation
- [x] Implement order book aggregation
- [x] Write matching engine tests

**Deliverables:**
- Order placement API
- Basic matching working
- Order book display

## Phase 4: Settlement & Currency (Week 7-8)

### Week 7: Market Settlement
- [x] Implement event resolution logic
- [x] Add atomic and idempotent market settlement workflow
- [x] Calculate and distribute exact, per-currency payouts
- [x] Update wallet balances on settlement
- [x] Add settlement history
- [x] Write settlement unit and PostgreSQL concurrency tests
- [x] Trigger settlement from event resolution through NATS JetStream

**Deliverables:**
- Automatic settlement when events resolve
- Correct payout calculations
- Settlement history tracking

### Week 8: Currency & Internationalization
- [x] Implement Currency service
- [x] Add exchange rate fetching (external API)
- [x] Implement rate caching (Redis, 1hr TTL)
- [x] Add currency conversion logic
- [x] Support USD, EUR, CNY, JPY, GBP
- [x] Add timezone conversion utilities
- [x] Write currency tests

**Deliverables:**
- Multi-currency display
- Real-time exchange rates
- Timezone-aware timestamps

## Phase 5: Sports & Analytics (Week 9-10)

### Week 9: Sports Service
- [x] Implement Sports service
- [x] Integrate sports events from Polymarket
- [x] Add sports event sync job
- [x] Create sports event listing API
- [x] Add sports-specific filters
- [x] Write integration tests

**Deliverables:**
- Sports events syncing
- Sports event APIs
- League/team filtering

### Week 10: Analytics Service
- [x] Implement Analytics service
- [x] Add merchant statistics
- [x] Add market statistics
- [x] Add user statistics
- [x] Create aggregation queries
- [x] Add caching for expensive queries
- [x] Write analytics tests

**Deliverables:**
- Merchant dashboard stats
- Market performance metrics
- User trading history

## Phase 6: Testing & Polish (Week 11-12)

### Week 11: Integration Testing
- [x] Write end-to-end tests
  - [x] Full order flow
  - [x] Market settlement flow
  - [x] Multi-currency scenarios
- [x] Execute load testing baseline with k6
  - [x] Add authenticated read-path k6 scenario and thresholds
- [x] Fix bugs found in testing
- [x] Add error handling and deterministic fixture cleanup improvements

**Deliverables:**
- Comprehensive test suite
- Load test results (25 VU, 0% failures, p95 12.73 ms locally)
- Bug fixes

### Week 12: Documentation & Deployment
- [x] Write API documentation (OpenAPI)
- [x] Create deployment guides
- [x] Setup Kubernetes manifests
- [x] Document monitoring as post-MVP (Prometheus/Grafana are not runtime dependencies)
- [x] Setup logging (structured JSON)
- [x] Create runbooks for operations

**Deliverables:**
- Complete API documentation
- Deployment-ready K8s manifests
- Monitoring boundary documented for a later production observability phase
- Operational runbooks

## Success Criteria

### Must Have (P0)
- ✅ Merchant registration and API authentication
- ✅ Event syncing from Polymarket
- ✅ Market creation by admin
- ✅ Order placement and matching
- ✅ Virtual wallet with balance tracking
- ✅ Automatic market settlement
- ✅ Multi-currency display

### Should Have (P1)
- ✅ Analytics dashboard APIs
- ✅ Sports event integration
- Order history and filtering
- Transaction audit trail
- Basic rate limiting

### Nice to Have (P2)
- WebSocket real-time updates
- Advanced order types (limit orders)
- Market maker incentives
- Detailed performance metrics

## Risk Mitigation

### Technical Risks
1. **Polymarket API rate limits**
   - Mitigation: Implement exponential backoff, cache aggressively
   
2. **Order matching performance**
   - Mitigation: Use Redis for order book, optimize queries
   
3. **Wallet balance consistency**
   - Mitigation: Use database transactions, implement idempotency

### Schedule Risks
1. **Underestimated complexity**
   - Mitigation: Cut P2 features if needed, focus on P0/P1
   
2. **Integration issues**
   - Mitigation: Start Polymarket integration early, have mocks ready

## Next Steps

1. Run `twill generate` to generate Twill boilerplate
2. Implement database layer and repository pattern
3. Start with Merchant service (simplest component)
4. Build incrementally, test continuously
5. Deploy early and often to staging environment

## Current Status

✅ Weeks 1-12 MVP implementation complete
✅ Full race, integration, HTTP E2E, Docker, and deployment validation passed
✅ PostgreSQL, Redis, and NATS JetStream are the only required MVP infrastructure
⏳ Next: choose a staging environment and replace example production secrets/hostnames
