#!/usr/bin/env python3
"""
Squid Per-Site Metrics Exporter

This script parses Squid access logs to generate per-site hit/miss rate metrics
and exposes them in Prometheus format.

Key metrics exposed:
- squid_site_requests_total{site="example.com", status="hit|miss"}
- squid_site_hit_ratio{site="example.com"}
"""

import os
import re
import time
import threading
from collections import defaultdict
from urllib.parse import urlparse
from datetime import datetime
from http.server import HTTPServer, BaseHTTPRequestHandler
import json

class SquidLogParser:
    def __init__(self, log_path):
        self.log_path = log_path
        self.metrics = defaultdict(lambda: defaultdict(int))
        self.response_times = defaultdict(list)
        self.last_position = 0
        
        # Squid log format regex
        self.log_pattern = re.compile(
            r'(\d+\.\d+)\s+(\d+)\s+(\S+)\s+(\S+)/(\d+)\s+(\d+)\s+(\S+)\s+(\S+)\s+(\S+)\s+(\S+)/(\S+)\s+(\S+)'
        )
        
    def parse_log_line(self, line):
        """Parse a single Squid log line."""
        match = self.log_pattern.match(line.strip())
        if not match:
            return None
            
        try:
            timestamp, elapsed, client_ip, code_status, status_code, size, method, url, rfc931, peer_status, peer_host, content_type = match.groups()
            
            parsed_url = urlparse(url)
            site = parsed_url.netloc.lower()
            
            if ':' in site:
                site = site.split(':')[0]
            if site.startswith('www.'):
                site = site[4:]
                
            hit_status = "hit" if "HIT" in code_status else "miss"
            
            return {
                'timestamp': float(timestamp),
                'elapsed': int(elapsed),
                'status_code': int(status_code),
                'size': int(size),
                'site': site,
                'hit_status': hit_status,
            }
        except (ValueError, AttributeError, IndexError):
            return None
    
    def update_metrics(self, parsed_line):
        """Update metrics based on parsed log line."""
        site = parsed_line['site']
        if not site or site == '-' or site == '':
            return
            
        self.metrics[site][f'requests_{parsed_line["hit_status"]}'] += 1
        self.metrics[site]['requests_total'] += 1
        
        if len(self.response_times[site]) >= 1000:
            self.response_times[site].pop(0)
        self.response_times[site].append(parsed_line['elapsed'])
    
    def tail_log(self):
        """Continuously tail the log file and parse new lines."""
        while True:
            try:
                with open(self.log_path, 'r') as f:
                    if self.last_position == 0:
                        f.seek(0, 2)
                        self.last_position = f.tell()
                    else:
                        f.seek(self.last_position)
                    
                    while True:
                        line = f.readline()
                        if line:
                            parsed = self.parse_log_line(line)
                            if parsed:
                                self.update_metrics(parsed)
                            self.last_position = f.tell()
                        else:
                            time.sleep(1)
                            
            except FileNotFoundError:
                print(f"Log file {self.log_path} not found. Waiting...")
                time.sleep(5)
            except Exception as e:
                print(f"Error reading log file: {e}")
                time.sleep(5)
    
    def get_prometheus_metrics(self):
        """Generate Prometheus format metrics."""
        metrics_output = []
        
        metrics_output.extend([
            "# HELP squid_site_requests_total Total number of requests per site",
            "# TYPE squid_site_requests_total counter",
        ])
        
        for site, site_metrics in self.metrics.items():
            if not site or site == '-':
                continue
                
            for status in ['hit', 'miss']:
                count = site_metrics.get(f'requests_{status}', 0)
                metrics_output.append(f'squid_site_requests_total{{site="{site}",status="{status}"}} {count}')

        
        metrics_output.extend([
            "",
            "# HELP squid_site_hit_ratio Cache hit ratio per site",
            "# TYPE squid_site_hit_ratio gauge",
        ])
        
        for site, site_metrics in self.metrics.items():
            if not site or site == '-':
                continue
                
            hits = site_metrics.get('requests_hit', 0)
            total = site_metrics.get('requests_total', 0)
            
            if total > 0:
                hit_ratio = hits / total
                metrics_output.append(f'squid_site_hit_ratio{{site="{site}"}} {hit_ratio:.3f}')
        
        return '\n'.join(metrics_output)

class MetricsHandler(BaseHTTPRequestHandler):
    def __init__(self, parser, *args, **kwargs):
        self.parser = parser
        super().__init__(*args, **kwargs)
    
    def do_GET(self):
        if self.path == '/metrics':
            metrics = self.parser.get_prometheus_metrics()
            self.send_response(200)
            self.send_header('Content-Type', 'text/plain; charset=utf-8')
            self.end_headers()
            self.wfile.write(metrics.encode('utf-8'))
        elif self.path == '/health':
            self.send_response(200)
            self.send_header('Content-Type', 'application/json')
            self.end_headers()
            health_data = {
                'status': 'healthy',
                'sites_monitored': len(self.parser.metrics),
                'last_update': datetime.now().isoformat(),
            }
            self.wfile.write(json.dumps(health_data).encode('utf-8'))
        else:
            self.send_response(404)
            self.end_headers()
    
    def log_message(self, format, *args):
        return

def main():
    log_path = os.environ.get('SQUID_LOG_PATH', '/var/log/squid/access.log')
    metrics_port = int(os.environ.get('METRICS_PORT', '9302'))
    
    print(f"Starting Squid per-site metrics exporter on port {metrics_port}")
    
    parser = SquidLogParser(log_path)
    
    log_thread = threading.Thread(target=parser.tail_log, daemon=True)
    log_thread.start()
    
    def handler(*args, **kwargs):
        return MetricsHandler(parser, *args, **kwargs)
    
    server = HTTPServer(('0.0.0.0', metrics_port), handler)
    
    try:
        server.serve_forever()
    except KeyboardInterrupt:
        print("Shutting down...")
        server.shutdown()

if __name__ == '__main__':
    main() 
