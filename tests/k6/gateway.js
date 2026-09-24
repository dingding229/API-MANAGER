import http from 'k6/http';
import { check, sleep } from 'k6';

const baseURL = __ENV.API_MANAGER_URL || 'http://localhost:8080';
const apiPath = __ENV.API_PATH || '/api/health';
const apiKey = __ENV.API_KEY || '';

export const options = {
  scenarios: {
    gateway_load: {
      executor: 'ramping-arrival-rate',
      startRate: Number(__ENV.START_RATE || 5),
      timeUnit: '1s',
      preAllocatedVUs: Number(__ENV.PREALLOCATED_VUS || 10),
      maxVUs: Number(__ENV.MAX_VUS || 50),
      stages: [
        { target: Number(__ENV.TARGET_RATE || 50), duration: __ENV.RAMP_UP || '30s' },
        { target: Number(__ENV.TARGET_RATE || 50), duration: __ENV.HOLD || '1m' },
        { target: 0, duration: __ENV.RAMP_DOWN || '15s' },
      ],
    },
  },
  thresholds: {
    http_req_failed: ['rate<0.01'],
    http_req_duration: ['p(95)<1000'],
  },
};

export default function () {
  const headers = apiKey ? { 'X-API-Key': apiKey } : {};
  const response = http.get(`${baseURL}${apiPath}`, { headers, tags: { name: 'gateway' } });
  check(response, {
    'gateway returned 2xx': (r) => r.status >= 200 && r.status < 300,
  });
  sleep(0.1);
}
