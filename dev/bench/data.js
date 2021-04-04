window.BENCHMARK_DATA = {
  "lastUpdate": 1617559315556,
  "repoUrl": "https://github.com/mgnsk/evcache",
  "entries": {
    "Benchmark": [
      {
        "commit": {
          "author": {
            "email": "magnus@kokk.eu",
            "name": "Magnus Kokk",
            "username": "mgnsk"
          },
          "committer": {
            "email": "noreply@github.com",
            "name": "GitHub",
            "username": "web-flow"
          },
          "distinct": true,
          "id": "32d8e678763215451dd4e2e63725d8ecb124163b",
          "message": "Update go.yml",
          "timestamp": "2021-04-04T21:00:45+03:00",
          "tree_id": "566c7299634067c05a69a7c67018dacd1d194484",
          "url": "https://github.com/mgnsk/evcache/commit/32d8e678763215451dd4e2e63725d8ecb124163b"
        },
        "date": 1617559314703,
        "tool": "go",
        "benches": [
          {
            "name": "BenchmarkCapacityParallel",
            "value": 1172,
            "unit": "ns/op\t     234 B/op\t       6 allocs/op",
            "extra": "1000000 times\n2 procs"
          },
          {
            "name": "BenchmarkFetchAndEvictParallel",
            "value": 469.6,
            "unit": "ns/op\t      40 B/op\t       1 allocs/op",
            "extra": "2483325 times\n2 procs"
          },
          {
            "name": "BenchmarkGet",
            "value": 315.2,
            "unit": "ns/op\t       0 B/op\t       0 allocs/op",
            "extra": "4174971 times\n2 procs"
          },
          {
            "name": "BenchmarkSetNotExists",
            "value": 1536,
            "unit": "ns/op\t     283 B/op\t       5 allocs/op",
            "extra": "1000000 times\n2 procs"
          },
          {
            "name": "BenchmarkSetExists",
            "value": 773.1,
            "unit": "ns/op\t     160 B/op\t       4 allocs/op",
            "extra": "1568656 times\n2 procs"
          },
          {
            "name": "BenchmarkFetchExists",
            "value": 400.1,
            "unit": "ns/op\t       7 B/op\t       0 allocs/op",
            "extra": "3002070 times\n2 procs"
          },
          {
            "name": "BenchmarkFetchNotExists",
            "value": 1524,
            "unit": "ns/op\t     283 B/op\t       5 allocs/op",
            "extra": "1000000 times\n2 procs"
          },
          {
            "name": "BenchmarkPop",
            "value": 593.8,
            "unit": "ns/op\t      16 B/op\t       1 allocs/op",
            "extra": "2552797 times\n2 procs"
          }
        ]
      }
    ]
  }
}