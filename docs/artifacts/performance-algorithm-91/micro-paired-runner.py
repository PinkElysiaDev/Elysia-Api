from pathlib import Path
import subprocess,os,hashlib,json,sys
out=Path('/home/zed/workspace/elysia-api/.tmp-dev/perf-algorithm/validation')
env=dict(os.environ,GOMAXPROCS='4',GOGC='100',GOMEMLIMIT='2GiB')
results=[]
prefix=sys.argv[1] if len(sys.argv)>1 else 'item-3'
for i,label in enumerate(['before','after','after','before']):
 binary=Path('/tmp/opencode/algorithm-conversion-'+label+'.test')
 args=[str(binary),'-test.run=^$','-test.bench=^BenchmarkAlgorithmConversion$/nodes1024/rules(1|16)$','-test.benchmem','-test.benchtime=500ms','-test.count=3']
 p=subprocess.run(args,env=env,stdout=subprocess.PIPE,stderr=subprocess.STDOUT,text=True,timeout=180)
 (out/f'{prefix}-paired-micro-{i}-{label}.log').write_text(p.stdout)
 print(p.stdout)
 assert p.returncode==0
 results.append({'label':label,'sha256':hashlib.sha256(binary.read_bytes()).hexdigest(),'argv':args})
(out/f'{prefix}-paired-micro-identities.log').write_text(json.dumps(results,indent=2)+'\n')
