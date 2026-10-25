import { copyButtons, stars } from './chrome';
import { runtimeCycle } from './runtimes';
import { scratch } from './scratch';
import { terminals } from './terminals';

document.querySelectorAll<HTMLElement>('h1 s').forEach((el) => void scratch(el));
stars();
copyButtons();
terminals();
runtimeCycle();
