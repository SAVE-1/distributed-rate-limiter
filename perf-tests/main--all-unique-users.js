import http from 'k6/http';
import { check } from 'k6';
import { Counter } from 'k6/metrics'; // 1. Import Counter
import exec from 'k6/execution';

// 2. Define Custom Counters globally
const successCounter = new Counter('rate_limit_passes');
const deniedCounter = new Counter('rate_limit_denied');

export const options = {
    scenarios: {
        steady_rate: {
            executor: 'constant-arrival-rate',
            rate: 12000,
            timeUnit: '1s',
            duration: '60s',
            preAllocatedVUs: 800,
            maxVUs: 1500,
        },
    },
    // Add thresholds for your new metrics if desired
    thresholds: {
        http_req_failed: ['rate<0.01'],
        http_req_duration: ['p(95)<25', 'p(99)<50'],
        // Example: Ensure denied requests are above 99%
        'rate_limit_denied': ['count>0'],
    },
};

export default function () {
    const clientId = `client-${(exec.vu.idInInstance * 100000) + exec.vu.iterationInScenario}`;
    
    console.log('clientId' + clientId);
    const payload = JSON.stringify({
        ClientId: clientId,
        RulesId: 'content-name',
    });

    const res = http.post(
        'http://localhost:8080/v1/ratelimit',
        payload,
        { headers: { 'Content-Type': 'application/json' } }
    );

    // 3. Increment the appropriate counter based on status
    if (res.status === 200) {
        successCounter.add(1);
    } else if (res.status === 429) {
        deniedCounter.add(1);
    }

    // Keep your check for overall validation
    check(res, {
        'status is 200 or 429': (r) => r.status === 200 || r.status === 429,
    });
}
