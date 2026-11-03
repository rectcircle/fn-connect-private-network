// Presentation-only projection of daemon timestamped measurements.
export class ThroughputTimeline {
 samples=[]; family=''; link=''; window=8000;
 reset(){this.samples=[];this.family='';this.link='';this.window=8000;}
 update(status){
  const family=status.stage?.startsWith('upload')?'upload':status.stage;
  if(status.link!==this.link||(['upload','download'].includes(family)&&family!==this.family))this.samples=[];
  this.link=status.link;
  if(!['upload','download'].includes(family))return;
  this.family=family;this.window=status.targetSampleMs>0?status.targetSampleMs:8000;
  const elapsed=status.sampleElapsedMs||0;
  if(elapsed<=0||elapsed<=(this.samples.at(-1)?.elapsed??-1))return;
  this.samples.push({elapsed,rate:Math.max(0,status.mbps||0)});
  const cutoff=Math.max(0,elapsed-this.window);
  while(this.samples.length>1&&this.samples[1].elapsed<=cutoff)this.samples.shift();
 }
 points(width=600){
  const maximum=Math.max(1,...this.samples.map(s=>s.rate)),origin=Math.max(0,(this.samples.at(-1)?.elapsed||0)-this.window);
  return this.samples.map((sample,index)=>{
   let {elapsed,rate}=sample;
   if(elapsed<origin&&index+1<this.samples.length){const next=this.samples[index+1];rate+=(next.rate-rate)*(origin-elapsed)/(next.elapsed-elapsed);elapsed=origin;}
   return `${(elapsed-origin)/this.window*width},${42-rate/maximum*36}`;
  }).join(' ');
 }
}
