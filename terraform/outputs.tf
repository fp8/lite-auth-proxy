output "service_urls" {
  description = "URLs for each deployed Cloud Run service"
  value = {
    for name, svc in google_cloud_run_v2_service.proxy :
    name => svc.uri
  }
}
