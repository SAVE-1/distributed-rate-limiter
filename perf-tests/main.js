import http from 'k6/http';
import { check } from 'k6';
import { __VU, __ITER } from 'k6/execution';

export const options = {
  scenarios: {
    steady_rate: {
      executor: 'constant-arrival-rate',
      rate: 12000,       // 20k requests/sec
      timeUnit: '1s',
      duration: '60s',
      preAllocatedVUs: 800,
      maxVUs: 1500,
    },
  },
  thresholds: {
    http_req_failed: ['rate<0.01'],
    http_req_duration: ['p(95)<25', 'p(99)<50'],
  },
};

export default function () {
  const clientId = `client-${(__VU * 100000) + __ITER}`;

  const payload = JSON.stringify({
    ClientId: clientId,
    RulesId: 'content-name',
  });

  const res = http.post(
    'http://localhost:8080/v1/ratelimit',
    payload,
    { headers: { 'Content-Type': 'application/json' } }
  );

  check(res, {
    'status is 200 or 429': (r) => r.status === 200 || r.status === 429,
  });
}