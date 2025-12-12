import http from 'k6/http';
import { check } from 'k6';

export const options = {
  vus: 10, // Virtual Users
  duration: '10s',
};

export default function () {
  const url = `http://localhost:8080/v1/ratelimit`;
  
  const payload = JSON.stringify({
    ClientId: '1-1-1-1-1',
    RulesId: 'content name',
  });

  const params = {
    headers: {
      'Content-Type': 'application/json',
      'Accept-Language': 'en-US,en;q=0.5',
    },
  };

  const res = http.post(url, payload, params);
  
  // Track how many requests were allowed (200) vs limited (429)
  check(res, {
    'is status 200': (r) => r.status === 200,
    'is status 429': (r) => r.status === 429,
  });
}
