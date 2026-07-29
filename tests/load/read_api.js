import http from 'k6/http';
import { check, sleep } from 'k6';

export const options = {
  scenarios: {
    read_api: {
      executor: 'ramping-vus',
      stages: [
        { duration: '10s', target: 10 },
        { duration: '30s', target: 25 },
        { duration: '10s', target: 0 },
      ],
    },
  },
  thresholds: {
    http_req_failed: ['rate<0.01'],
    http_req_duration: ['p(95)<250'],
  },
};

const baseURL = __ENV.BASE_URL || 'http://localhost:8080';
const apiKey = __ENV.API_KEY;
const marketID = __ENV.MARKET_ID;

export default function () {
  const headers = { Authorization: `Bearer ${apiKey}` };
  const responses = http.batch([
    ['GET', `${baseURL}/api/v1/events?status=active&limit=20`, null, { headers }],
    ['GET', `${baseURL}/api/v1/markets?status=active&limit=20`, null, { headers }],
    ['GET', `${baseURL}/api/v1/analytics/merchant?time_range=7d`, null, { headers }],
  ]);
  if (marketID) {
    responses.push(http.get(`${baseURL}/api/v1/markets/${marketID}/orderbook`, { headers }));
  }
  for (const response of responses) {
    check(response, { 'status is 200': (value) => value.status === 200 });
  }
  sleep(1);
}
