import {openNativeSettings} from './settings.js';
import {Ready} from '../wailsjs/go/main/App';
await openNativeSettings();
await Ready();
