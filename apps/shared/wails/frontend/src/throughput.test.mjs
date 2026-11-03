import test from 'node:test';
import assert from 'node:assert/strict';
import {ThroughputTimeline} from './throughput.js';
test('daemon time positions are independent of delivery cadence and scroll after the sampling window',()=>{
 const graph=new ThroughputTimeline();
 for(const elapsed of [250,1000,4000,8000])graph.update({stage:'upload',link:'fn-connect',sampleElapsedMs:elapsed,targetSampleMs:8000,mbps:4});
 assert.equal(graph.points(320).split(' ')[1].split(',')[0],'40');
 assert.equal(graph.points(320).split(' ').at(-1).split(',')[0],'320');
 graph.update({stage:'upload-confirming',link:'fn-connect',sampleElapsedMs:8000,targetSampleMs:8000,mbps:4});assert.equal(graph.samples.length,4);
 graph.update({stage:'upload',link:'fn-connect',sampleElapsedMs:10000,targetSampleMs:8000,mbps:4});assert.equal(graph.points(320).split(' ').at(-1).split(',')[0],'320');
});
