# Connect to Prometheus and AlertManager using port forwarding

```bash
kubectl port-forward -n monitoring service/alertmanager-operated 9093:9093
```

```bash
kubectl port-forward -n monitoring service/prometheus-operated 9090:9090
```
